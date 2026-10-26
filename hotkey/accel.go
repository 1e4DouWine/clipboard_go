// Package hotkey 负责全局快捷键：语法校验、注册与改键、冲突处理、自定义持久化。
//
// 设计要点：
//   - 语法与 mygo 的 accelerator.Parse 保持一致（Electron 风格，如 "Ctrl+Shift+V"）；
//   - 额外施加"全局安全策略"：会劫持日常输入的按键必须携带 Ctrl/Alt/Super 修饰键，
//     防止用户把裸字母/裸方向键设成全局热键后无法正常打字；
//   - Backend 抽象 mygo.GlobalShortcut（其方法集天然满足该接口），测试注入 fake，
//     不依赖 mygo 应用运行环境；
//   - 触发回调（handler）由 mygo 在主线程调用，可安全操作 UI；但因此 handler 内
//     不得同步调用 Manager 的注册/注销方法（见 manager.go 的锁说明）。
package hotkey

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// 包级错误，调用方用 errors.Is 判断并映射到 UI 提示。
var (
	// ErrInvalidAccelerator 表示快捷键语法非法（拼写错误、缺按键、未知按键名等）。
	ErrInvalidAccelerator = errors.New("hotkey: 无效的快捷键")
	// ErrUnsafeAccelerator 表示语法合法但违反全局安全策略
	//（例如裸字母键会劫持日常输入，必须携带 Ctrl/Alt/Super）。
	ErrUnsafeAccelerator = errors.New("hotkey: 不安全的全局快捷键")
	// ErrConflict 表示快捷键冲突：已被其他 action 注册，或被系统/其他程序占用。
	ErrConflict = errors.New("hotkey: 快捷键冲突")
	// ErrUnknownAction 表示 action 未注册或没有默认配置。
	ErrUnknownAction = errors.New("hotkey: 未知操作")
)

// parsed 是归一化后的加速键：修饰键布尔位 + 规范键名。
type parsed struct {
	super, ctrl, alt, shift bool
	key                     string
}

// hasPrimaryMod 报告是否携带 Ctrl/Alt/Super 之一（Shift 不算安全修饰键，
// 因为 Shift+字母 仍是日常输入的一部分）。
func (p parsed) hasPrimaryMod() bool { return p.super || p.ctrl || p.alt }

// canon 生成规范字符串（顺序：Super、Ctrl、Alt、Shift、按键；单字符按键大写），
// 与 mygo 的 Accelerator.String() 规则一致，保证冲突检测与 mygo 内部表对齐。
func (p parsed) canon() string {
	parts := make([]string, 0, 5)
	if p.super {
		parts = append(parts, "Super")
	}
	if p.ctrl {
		parts = append(parts, "Ctrl")
	}
	if p.alt {
		parts = append(parts, "Alt")
	}
	if p.shift {
		parts = append(parts, "Shift")
	}
	key := p.key
	if len(key) == 1 { // 与 mygo 一致按字节长度判断，仅 ASCII 单字符转大写
		key = strings.ToUpper(key)
	}
	parts = append(parts, key)
	return strings.Join(parts, "+")
}

// namedKeys 是命名键的别名表（小写别名 → 规范键名），与 mygo 的命名键集合一致。
var namedKeys = map[string]string{
	"plus": "+", "space": "Space", "tab": "Tab",
	"capslock": "CapsLock", "numlock": "NumLock", "scrolllock": "ScrollLock",
	"backspace": "Backspace", "delete": "Delete", "del": "Delete",
	"insert": "Insert", "ins": "Insert",
	"return": "Enter", "enter": "Enter",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pageup": "PageUp", "pagedown": "PageDown",
	"escape": "Escape", "esc": "Escape",
	"volumeup": "VolumeUp", "volumedown": "VolumeDown", "volumemute": "VolumeMute",
	"medianexttrack": "MediaNextTrack", "mediaprevioustrack": "MediaPreviousTrack",
	"mediastop": "MediaStop", "mediaplaypause": "MediaPlayPause",
	"printscreen": "PrintScreen",
	"numdec":      "NumDec", "numadd": "NumAdd", "numsub": "NumSub",
	"nummult": "NumMult", "numdiv": "NumDiv",
}

// toModifier 把修饰键名称归一化；第二个返回值表示是否为修饰键。
// CmdOrCtrl 按平台解析：darwin 上为 Super（Command），其余平台为 Ctrl。
func toModifier(p, goos string) (super, ctrl, alt, shift, ok bool) {
	switch strings.ToLower(p) {
	case "command", "cmd", "super", "meta", "win":
		return true, false, false, false, true
	case "control", "ctrl":
		return false, true, false, false, true
	case "commandorcontrol", "cmdorctrl":
		if goos == "darwin" {
			return true, false, false, false, true
		}
		return false, true, false, false, true
	case "alt", "option", "altgr":
		return false, false, true, false, true
	case "shift":
		return false, false, false, true, true
	}
	return false, false, false, false, false
}

// parseKeyName 解析按键名（单字符 / 命名键 / F1-F24 / Num0-Num9）。
// 规则与 mygo 的 parseKey 保持一致。
func parseKeyName(p string) (string, bool) {
	if len([]rune(p)) == 1 {
		return strings.ToLower(p), true // 单字符键（字母/数字/标点，含 "+"）
	}
	lower := strings.ToLower(p)
	if k, ok := namedKeys[lower]; ok {
		return k, true
	}
	// F1-F24：形如 "f12"；拒绝 "f01" 之类的非规范写法（与 mygo 一致）。
	if len(lower) >= 2 && lower[0] == 'f' {
		n, err := strconv.Atoi(lower[1:])
		if err == nil && n >= 1 && n <= 24 && strconv.Itoa(n) == lower[1:] {
			return "F" + strconv.Itoa(n), true
		}
	}
	// 数字小键盘：形如 "num5"。
	if strings.HasPrefix(lower, "num") && len(lower) == 4 && lower[3] >= '0' && lower[3] <= '9' {
		return "Num" + string(lower[3]), true
	}
	return "", false
}

// splitPlus 按 "+" 切分，但允许 "+" 本身作为按键：
// "Ctrl++" → ["Ctrl", "+"], 单独的 "+" → ["+"]。
func splitPlus(s string) []string {
	parts := strings.Split(s, "+")
	if strings.HasSuffix(s, "++") {
		return append(parts[:len(parts)-2], "+")
	}
	if s == "+" {
		return []string{"+"}
	}
	return parts
}

// parseAcc 解析并归一化加速键；goos 影响 CmdOrCtrl 的解析结果。
// 错误统一包装为 ErrInvalidAccelerator，便于 UI 层归类提示。
func parseAcc(s, goos string) (parsed, error) {
	var out parsed
	if strings.TrimSpace(s) == "" {
		return out, fmt.Errorf("%w: 不能为空", ErrInvalidAccelerator)
	}
	parts := splitPlus(s)
	for i, raw := range parts {
		p := strings.TrimSpace(raw)
		if p == "" {
			return out, fmt.Errorf("%w: %q 含空段", ErrInvalidAccelerator, s)
		}
		last := i == len(parts)-1
		// 修饰键必须出现在非末位；末位留给实际按键。
		if su, c, a, sh, ok := toModifier(p, goos); ok && !last {
			out.super, out.ctrl, out.alt, out.shift =
				out.super || su, out.ctrl || c, out.alt || a, out.shift || sh
			continue
		}
		if !last {
			return out, fmt.Errorf("%w: %q 中的 %q 不是修饰键", ErrInvalidAccelerator, s, p)
		}
		key, ok := parseKeyName(p)
		if !ok {
			return out, fmt.Errorf("%w: %q 中的按键 %q 无法识别", ErrInvalidAccelerator, s, p)
		}
		out.key = key
	}
	return out, nil
}

// keyNeedsModifier 判定按键是否必须携带 Ctrl/Alt/Super 才允许作为全局热键。
//
// 策略：只有系统级功能键（F1-F24、PrintScreen、音量/媒体键）可以裸按；
// 字母、数字、标点、方向键、Enter/Tab/Space 等一旦被全局占用会劫持日常输入。
func keyNeedsModifier(key string) bool {
	switch key {
	case "PrintScreen", "VolumeUp", "VolumeDown", "VolumeMute",
		"MediaNextTrack", "MediaPreviousTrack", "MediaStop", "MediaPlayPause":
		return false
	}
	if strings.HasPrefix(key, "F") { // F1-F24
		if _, err := strconv.Atoi(key[1:]); err == nil {
			return false
		}
	}
	return true
}

// Validate 校验快捷键：语法 + 全局安全策略。
// 语法非法返回 ErrInvalidAccelerator；违反安全策略返回 ErrUnsafeAccelerator。
func Validate(acc string) error {
	_, err := normalize(acc)
	return err
}

// Canonical 返回快捷键的规范形式（如 "ctrl+shift+v" → "Ctrl+Shift+V"），
// 可用于存储与展示；语法非法时返回 ErrInvalidAccelerator。
// 注意：Canonical 只管语法，不做安全策略检查（存储历史值时可宽松）。
func Canonical(acc string) (string, error) {
	p, err := parseAcc(acc, runtime.GOOS)
	if err != nil {
		return "", err
	}
	return p.canon(), nil
}

// normalize 是 Register 内部使用的组合校验：安全策略 + 规范化。
func normalize(acc string) (string, error) {
	p, err := parseAcc(acc, runtime.GOOS)
	if err != nil {
		return "", err
	}
	if keyNeedsModifier(p.key) && !p.hasPrimaryMod() {
		return "", fmt.Errorf("%w: %q 会劫持日常输入，必须携带 Ctrl/Alt/Super 修饰键", ErrUnsafeAccelerator, acc)
	}
	return p.canon(), nil
}
