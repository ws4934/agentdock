package desktop

import (
	"github.com/uvwt/agentdock/internal/tool/core"
	"strings"
)

// 快捷键使用 macOS 虚拟键码；任意语言文本应走 Unicode text，不依赖剪贴板。
var keyCodes = map[string]uint16{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9, "b": 11,
	"q": 12, "w": 13, "e": 14, "r": 15, "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21,
	"6": 22, "5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29,
	"]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35, "enter": 36, "return": 36, "l": 37, "j": 38,
	"'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44, "n": 45, "m": 46, ".": 47,
	"tab": 48, "space": 49, "`": 50, "backspace": 51, "escape": 53, "esc": 53,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97, "f7": 98, "f8": 100,
	"f9": 101, "f10": 109, "f11": 103, "f12": 111, "home": 115, "end": 119, "pageup": 116,
	"pagedown": 121, "delete": 117, "left": 123, "right": 124, "down": 125, "up": 126,
}

func modifierFlags(modifiers []string) (uint64, error) {
	var flags uint64
	if len(modifiers) > 4 {
		return 0, invalid("at most four modifiers are allowed")
	}
	for _, m := range modifiers {
		var flag uint64
		switch strings.ToLower(m) {
		case "shift":
			flag = 1 << 17
		case "control", "ctrl":
			flag = 1 << 18
		case "option", "alt":
			flag = 1 << 19
		case "command", "cmd", "meta":
			flag = 1 << 20
		default:
			return 0, invalid("unknown modifier: " + m)
		}
		if flags&flag != 0 {
			return 0, invalid("duplicate modifier")
		}
		flags |= flag
	}
	return flags, nil
}
func invalid(message string) error { return core.NewError("INVALID_ARGUMENT", message, "validation") }
