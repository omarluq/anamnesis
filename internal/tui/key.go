package tui

import (
	"strings"

	"github.com/gdamore/tcell/v3"
)

const keyCtrlR = "ctrl+r"

// KeyEvent is a normalized terminal key event.
type KeyEvent struct {
	Key  string
	Text string
	Ctrl bool
}

// NewKeyEvent converts a tcell/v3 key event into a normalized key event.
func NewKeyEvent(event *tcell.EventKey) (KeyEvent, bool) {
	if event == nil {
		return emptyKeyEvent(), false
	}

	if event.Key() == tcell.KeyRune {
		return runeKeyEvent(event), true
	}

	key, ok := specialKeyName(event.Key())
	if !ok {
		return emptyKeyEvent(), false
	}

	return KeyEvent{
		Key:  key,
		Text: "",
		Ctrl: strings.HasPrefix(key, "ctrl+") || event.Modifiers()&tcell.ModCtrl != 0,
	}, true
}

func emptyKeyEvent() KeyEvent {
	return KeyEvent{Key: "", Text: "", Ctrl: false}
}

func runeKeyEvent(event *tcell.EventKey) KeyEvent {
	text := event.Str()
	ctrl := event.Modifiers()&tcell.ModCtrl != 0

	key := text
	if ctrl {
		key = "ctrl+" + strings.ToLower(text)
	}

	return KeyEvent{
		Key:  key,
		Text: text,
		Ctrl: ctrl,
	}
}

func specialKeyName(key tcell.Key) (string, bool) {
	keyNames := map[tcell.Key]string{
		tcell.KeyEscape:     "escape",
		tcell.KeyEnter:      "enter",
		tcell.KeyTab:        "tab",
		tcell.KeyBacktab:    "shift+tab",
		tcell.KeyBackspace:  "backspace",
		tcell.KeyBackspace2: "backspace",
		tcell.KeyDelete:     "delete",
		tcell.KeyLeft:       "left",
		tcell.KeyRight:      "right",
		tcell.KeyUp:         "up",
		tcell.KeyDown:       "down",
		tcell.KeyHome:       "home",
		tcell.KeyEnd:        "end",
		tcell.KeyPgUp:       "pageup",
		tcell.KeyPgDn:       "pagedown",
		tcell.KeyCtrlA:      "ctrl+a",
		tcell.KeyCtrlB:      "ctrl+b",
		tcell.KeyCtrlC:      "ctrl+c",
		tcell.KeyCtrlE:      "ctrl+e",
		tcell.KeyCtrlF:      "ctrl+f",
		tcell.KeyCtrlK:      "ctrl+k",
		tcell.KeyCtrlO:      "ctrl+o",
		tcell.KeyCtrlR:      keyCtrlR,
		tcell.KeyCtrlT:      "ctrl+t",
		tcell.KeyCtrlU:      "ctrl+u",
		tcell.KeyCtrlW:      "ctrl+w",
	}
	name, ok := keyNames[key]

	return name, ok
}
