package main

import (
	"context"
	"errors"
	"log"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"my-go-app/applog"
	"my-go-app/hotkey"
	"my-go-app/store"
	"my-go-app/theme"
	"my-go-app/watch"
)

// main 组装应用：持久化 → 后台监听 → 全局快捷键 → 窗口，随后进入事件循环。
//
// 组装原则：app 只依赖接口（clipWriter/windowOps/paster），生产实现全部在
// 这里注入；任一组件初始化失败都按"降级不阻塞"处理——例如全局快捷键被占用
// 时仅提示改键，应用本身照常可用。
func main() {
	// 单实例保护：剪贴板应用不允许双开（否则重复捕获、热键互相抢占）。
	// 已有实例在运行时，本进程静默退出。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	// 应用名先行：影响 mygo.App.Path 系列目录名（日志/数据目录）。
	mygo.App.SetName("Clipboard History")

	// 日志先行：落盘到用户日志目录，之后所有初始化/运行日志均可追溯。
	// 日志不可用时降级为仅 stderr，不阻止应用启动。
	logPath := setupLogging()
	log.Printf("启动 Clipboard History: version=%s go=%s os=%s/%s log=%s",
		mygo.App.Version(), runtime.Version(), runtime.GOOS, runtime.GOARCH, logPath)

	a := &app{paste: fastPaster{}}

	mygo.App.OnWindowAllClosed(func() {}) // 关窗不退出：后台监听继续工作
	mygo.App.OnSecondInstance(func([]string, string) {
		// 用户再次启动程序：把已有窗口带到前台（视为唤起手势）。
		a.toggleWindow()
	})

	mygo.App.WhenReady(func() {
		if err := a.setup(); err != nil {
			// 初始化失败属于致命错误：记录后退出，避免半可用状态。
			log.Fatalf("初始化失败: %v", err)
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupLogging 初始化落盘日志，返回日志文件路径（失败返回空串，降级仅 stderr）。
func setupLogging() string {
	// PathLogs 是 mygo 约定的用户日志目录（如 %AppData%/Clipboard History/logs）。
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		log.Printf("定位日志目录失败（降级为仅 stderr）: %v", err)
		return ""
	}
	path, err := applog.Setup(dir)
	if err != nil {
		log.Printf("日志初始化失败（降级为仅 stderr）: %v", err)
		return ""
	}
	return path
}

// setup 在应用就绪后初始化各组件（主线程执行一次）。
func (a *app) setup() error {
	// ── 1. 持久化层 ──────────────────────────────────────────────
	// 数据存放在用户数据目录（%AppData%/Clipboard History），卸载可整体清除。
	dataDir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return errors.New("定位用户数据目录失败: " + err.Error())
	}
	st, err := store.Open(dataDir, store.Options{})
	if err != nil {
		return err // 损坏的库已在 store 内部备份重建，仍失败属于磁盘级故障
	}
	a.st = st
	a.clip = mygo.Clipboard // mygo.Clipboard 天然满足 clipWriter/watch.Clipboard
	a.login = mygo.App      // mygo.App 天然满足 loginOps（开机自启）

	// ── 1a. 语言：恢复界面语言偏好（默认英文） ─────────────────
	lang = LoadLang(st)
	a.langIdx = lang.Index()
	applyMygoLocale() // 同步控件 chrome（Search 占位符等）

	// ── 1b. 主题：恢复外观偏好并立即应用 ─────────────────────────
	// 必须在创建窗口之前应用，让第一个窗口直接以正确外观呈现，
	// 避免"启动闪白再变黑"。theme.Mode 与 mygo.ThemeSource 取值一一对应。
	theming, err := theme.New(st, theme.ApplierFunc(func(m theme.Mode) {
		mygo.Theme.SetSource(mygo.ThemeSource(m))
	}))
	if err != nil {
		_ = st.Close()
		return err
	}
	a.theming = theming
	a.themeIdx = theming.Mode().Index() // 同步切换器显示

	// ── 2. 窗口 ─────────────────────────────────────────────────
	// Hidden 语义：由登录自启时静默进后台（不抢焦点、不打扰用户），
	// 用户手动启动时正常显示。开关状态与系统同步（UI 反映真实状态）。
	hidden := mygo.App.WasOpenedAtLogin()
	a.autoStart = mygo.App.OpenAtLogin()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     T("Clipboard History"), // 系统窗口标题随语言；SetName 保持产品标识
		Width:     560,
		Height:    520,
		MinWidth:  360,
		MinHeight: 280,
		StateKey:  "main", // 记忆窗口位置/大小/最大化状态
		Hidden:    hidden,
		Content:   ui.View(a.safeView), // safeView：渲染 panic 降级而非闪退
	})
	// 关窗隐藏而非退出：保持后台捕获（与托盘常驻类应用一致）。
	// 用户可用全局快捷键随时唤回；彻底退出走 Ctrl+Q / 托盘菜单（阶段 4）。
	win.OnClose(func(e *mygo.CloseEvent) {
		e.PreventDefault()
		win.Hide()
	})
	a.win = win

	// ── 3. 后台监听 ──────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	watcher, err := watch.New(watch.Options{
		Clipboard: mygo.Clipboard, // 天然满足 watch.Clipboard
		Store:     st,
		OnCapture: a.onCaptured, // 后台 goroutine → win.Update 投递主线程
		OnError:   func(err error) { log.Printf("历史入库失败: %v", err) },
	})
	if err != nil {
		cancel()
		_ = st.Close()
		return err
	}
	go func() {
		// goroutine 兜底：watch 循环 panic 时记录堆栈并退出，
		// 不拖垮主进程；捕获功能失效但应用仍可手动粘贴历史。
		defer applog.Recover("watch-loop")
		watcher.Run(ctx)
	}() // 退出路径见 OnQuit：cancel 后 Run 自行结束

	// ── 4. 全局快捷键 ────────────────────────────────────────────
	// mygo.GlobalShortcut 天然满足 hotkey.Backend。
	hotkeys, err := hotkey.New(mygo.GlobalShortcut)
	if err != nil {
		cancel()
		_ = st.Close()
		return err
	}
	used, err := hotkey.RegisterWithSettings(hotkeys, st, hotkey.ActionToggleWindow, func() {
		// 热键回调兜底：panic 记录后不崩溃，热键功能失效但应用存活。
		defer applog.Recover("hotkey-toggle")
		a.toggleWindow()
	})
	if err != nil {
		// 冲突/配置错误均不致命：记录并提示改键，应用内快捷键不受影响。
		log.Printf("全局快捷键注册失败（可在设置中改键）: %v", err)
		a.win.Update(func() {
			a.status = T("Failed to register global shortcut. Use another shortcut.")
		})
	} else {
		log.Printf("全局快捷键已注册: %s", used)
	}

	// ── 5. 退出清理 ──────────────────────────────────────────────
	mygo.App.OnQuit(func() {
		cancel()                // 停止 watch 轮询（Run 随 ctx 取消退出）
		hotkeys.UnregisterAll() // 归还全局快捷键
		if err := st.Close(); err != nil {
			log.Printf("关闭存储失败: %v", err)
		}
	})

	// 初始加载历史。
	a.reload()
	return nil
}
