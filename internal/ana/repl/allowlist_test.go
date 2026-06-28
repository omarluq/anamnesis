package repl_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/omarluq/anamnesis/internal/ana/repl"
)

// TestInterpreterDeniesHostEffectPackages is the security-critical negative test
// for the stdlib allow-list: interpreted source must not be able to reach any
// filesystem, process, network, runtime, or memory-unsafe package. Each case
// evaluates raw Go the way the controller would emit it; every one must fail to
// eval (compile/resolve), because the dangerous package is never bound into the
// engine. A regression here means the embedded VM is once again an arbitrary
// host-RCE surface — interpreted os.WriteFile / exec.Command would run on the
// host with the operator's privileges.
func TestInterpreterDeniesHostEffectPackages(t *testing.T) {
	t.Parallel()

	deniedWrite := `os.WriteFile(` + strconv.Quote(filepath.Join(t.TempDir(), "ana_denied")) +
		`, []byte("x"), 0644)`

	cases := []struct {
		name string
		src  string
	}{
		{"os.WriteFile auto-import", deniedWrite},
		{"os.ReadFile auto-import", `os.ReadFile("/etc/passwd")`},
		{"os explicit import", "import \"os\"\nos.Getpid()"},
		{"os/exec auto-import", `exec.Command("id").CombinedOutput()`},
		{"os/exec explicit import", "import \"os/exec\"\nexec.Command(\"id\").Run()"},
		{"syscall", "import \"syscall\"\nsyscall.Getpid()"},
		{"net", "import \"net\"\nnet.ParseIP(\"1.1.1.1\")"},
		{"net/http", "import \"net/http\"\nhttp.Get(\"http://example.com\")"},
		{"runtime", "import \"runtime\"\nruntime.NumGoroutine()"},
		{"unsafe", "import \"unsafe\"\nunsafe.Sizeof(0)"},
		{"plugin", "import \"plugin\"\nplugin.Open(\"x\")"},
		{"os/signal", "import \"os/signal\"\nsignal.Ignore()"},
		{"io/ioutil", "import \"io/ioutil\"\nioutil.ReadFile(\"/etc/passwd\")"},
		{"reflect not exposed", "import \"reflect\"\nreflect.TypeOf(0).String()"},
		{"unregistered package", `mystery.DoThing()`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			interpreter := repl.NewInterpreter()

			result, err := interpreter.Eval(testCase.name, testCase.src)
			require.Error(t, err, "interpreted %q must not eval — the package is not on the allow-list", testCase.name)
			assert.False(t, result.Retval.IsValid(), "a denied eval resolves no value")
		})
	}
}

// TestInterpreterOSWriteFileDoesNotTouchHostFS proves the negative test above is
// real and not merely asserting on an error string: even when the controller
// emits os.WriteFile, no file appears on the host filesystem, because os is never
// resolvable inside the VM. This is the direct regression guard for the confirmed
// C1 arbitrary-write RCE.
func TestInterpreterOSWriteFileDoesNotTouchHostFS(t *testing.T) {
	t.Parallel()

	probe := filepath.Join(t.TempDir(), "ana_c1_regression_probe")

	interpreter := repl.NewInterpreter()

	_, err := interpreter.Eval("probe", `os.WriteFile(`+strconv.Quote(probe)+`, []byte("pwned"), 0644)`)
	require.Error(t, err, "interpreted os.WriteFile must not eval")

	_, statErr := os.Stat(probe)
	require.ErrorIs(t, statErr, os.ErrNotExist, "no host file may be created by interpreted source")
}

// TestInterpreterAllowsSafeStdlibPackages is the positive half: every allow-listed
// package must still evaluate, under auto-import (no import statement, exactly how
// the controller writes code). A regression here means the allow-list is too tight
// and is breaking legitimate investigations.
func TestInterpreterAllowsSafeStdlibPackages(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
	}{
		{"fmt", `fmt.Sprintf("n=%d", 42)`},
		{"strings", `strings.ToUpper("hi")`},
		{"strconv", `strconv.Itoa(7)`},
		{"sort", `xs := []int{3, 1, 2}; sort.Ints(xs); xs[0]`},
		{"time", `time.Second.String()`},
		{"math", `math.Sqrt(16.0)`},
		{"regexp", `regexp.MustCompile("a+").MatchString("aaa")`},
		{"errors", `errors.New("boom").Error()`},
		{"bytes", `bytes.Contains([]byte("abc"), []byte("b"))`},
		{"unicode", `unicode.IsUpper('A')`},
		{"unicode/utf8", `utf8.RuneCountInString("héllo")`},
		{"encoding/json", `b, _ := json.Marshal([]int{1, 2}); string(b)`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			interpreter := repl.NewInterpreter()

			result, err := interpreter.Eval(testCase.name, testCase.src)
			require.NoError(t, err, "allow-listed %q must still eval", testCase.name)
			assert.True(t, result.Retval.IsValid(), "an allow-listed eval resolves a value")
		})
	}
}

// TestAllowListLeavesHostPackagesIntact proves the stdlib allow-list does not
// impede host-package registration: on one interpreter a registered host surface
// (journal.Query, the same seam RegisterSurface installs in production) stays
// callable while a denied stdlib package (os) stays unresolvable. The host
// packages register through a path independent of the stdlib bridge, so locking
// down the bridge must not collaterally break them.
func TestAllowListLeavesHostPackagesIntact(t *testing.T) {
	t.Parallel()

	interpreter := repl.NewInterpreter()

	surface := new(mockJournal)
	surface.On("Query", "ssh").Return([]fakeEntry{
		{Unit: unitSSH, Message: "Accepted publickey for omar", Priority: 6},
	})

	require.NoError(t, repl.RegisterSurface[journalQuerier](interpreter, "journal", surface))

	hostResult, err := interpreter.Eval("host", `len(journal.Query("ssh"))`)
	require.NoError(t, err, "the registered host surface must stay callable under the allow-list")
	require.True(t, hostResult.Retval.IsValid())
	assert.Equal(t, int64(1), hostResult.Retval.Int())

	_, denyErr := interpreter.Eval("deny", `os.Getpid()`)
	require.Error(t, denyErr, "os stays denied on the same interpreter the host surface is live on")

	surface.AssertExpectations(t)
}
