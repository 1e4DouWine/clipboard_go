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
	"my-go-app/i18n"
	"my-go-app/store"
	"my-go-app/theme"
	"my-go-app/watch"
)

// main 组装应用：持久化 → 后台监听 → 全局快捷键 → 窗口，随后进入事件循环。
//
// 组装原则：app 只依赖接口（clipWriter/windowOps/paster/quitOps），
// 生产实现全部在这里注入；任一组件初始化失败都按"降级不阻塞"处理——
// 例如全局快捷键被占用时仅提示改键，应用本身照常可用。
func main() {
	// 应用名先行：影响 mygo.App.Path 系列目录名（日志/数据目录），
	// 同时决定单实例锁的键——两者必须同源，否则会出现"锁的是 A、数据写在 B"。
	mygo.App.SetName("Clipboard History")

	// 单实例保护：剪贴板应用不允许双开（否则重复捕获、热键互相抢占）。
	// 已有实例在运行时，本进程静默退出。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

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
//
// 失败即整体回滚：任何一步出错都会走同一条 cleanup 路径释放已初始化的
// 资源（监听、快捷键、数据库），不留下半可用状态。
func (a *app) setup() (err error) {
	defer func() {
		if err != nil {
			a.cleanup()
		}
	}()

	// ── 1. 持久化层 ──────────────────────────────────────────────
	st, err := a.openStore()
	if err != nil {
		return err
	}
	a.st = st
	a.clip = mygo.Clipboard // mygo.Clipboard 天然满足 clipWriter/watch.Clipboard
	a.login = mygo.App      // mygo.App 天然满足 loginOps（开机自启）
	a.quitter = mygo.App    // mygo.App 天然满足 quitOps（退出应用）

	// ── 1a. 语言：恢复界面语言偏好（默认英文） ─────────────────
	a.locale = i18n.New(i18n.Load(st))
	a.langIdx = a.locale.Index()
	a.locale.ApplyMygo() // 同步控件 chrome（Search 占位符等）

	// ── 1b. 主题：恢复外观偏好并立即应用 ─────────────────────────
	// 必须在创建窗口之前应用，让第一个窗口直接以正确外观呈现，
	// 避免"启动闪白再变黑"。theme.Mode 与 mygo.ThemeSource 取值一一对应。
	theming, err := theme.New(st, theme.ApplierFunc(func(m theme.Mode) {
		mygo.Theme.SetSource(mygo.ThemeSource(m))
	}))
	if err != nil {
		return err
	}
	a.theming = theming
	a.themeIdx = theming.Mode().Index() // 同步切换器显示

	// ── 2. 窗口 ─────────────────────────────────────────────────
	a.createWindow()

	// ── 3. 后台监听 ──────────────────────────────────────────────
	if err := a.startWatcher(); err != nil {
		return err
	}

	// ── 4. 全局快捷键 ────────────────────────────────────────────
	a.registerHotkey()

	// ── 5. 退出流程 ──────────────────────────────────────────────
	a.registerQuitHooks()

	// 初始加载历史。
	a.reload()
	return nil
}

// openStore 打开用户数据目录下的历史库。
//
// 数据存放在用户数据目录（%AppData%/Clipboard History），卸载可整体清除。
func (a *app) openStore() (*store.Store, error) {
	dataDir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, errors.New("定位用户数据目录失败: " + err.Error())
	}
	st, err := store.Open(dataDir, store.Options{})
	if err != nil {
		// 损坏的库已在 store 内部备份重建，仍失败属于磁盘级故障。
		return nil, err
	}
	return st, nil
}

// createWindow 创建主窗口并接好关闭语义。
//
// Hidden 语义：由登录自启时静默进后台（不抢焦点、不打扰用户），
// 用户手动启动时正常显示。开关状态与系统同步（UI 反映真实状态）。
func (a *app) createWindow() {
	hidden := mygo.App.WasOpenedAtLogin()
	a.autoStart = mygo.App.OpenAtLogin()
	a.searchDelay = defaultSearchDebounce
	a.ui = a.newPresenter()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     a.t("Clipboard History"), // 系统窗口标题随语言；SetName 保持产品标识
		Width:     560,
		Height:    520,
		MinWidth:  360,
		MinHeight: 280,
		StateKey:  "main", // 记忆窗口位置/大小/最大化状态
		Hidden:    hidden,
		Content:   ui.View(a.ui.View),
	})
	// 关窗隐藏而非退出：保持后台捕获（与托盘常驻类应用一致）。
	// 用户可用全局快捷键随时唤回；彻底退出走 Ctrl+Q。
	// 退出流程由 OnBeforeQuit 标记后放行真实关闭（见 onWindowClose）。
	win.OnClose(a.onWindowClose)
	a.win = win
}

// startWatcher 启动后台剪贴板监听。
//
// 监听在后台 goroutine 阻塞运行，ctx 取消即退出（见 cleanup）。
func (a *app) startWatcher() error {
	ctx, cancel := context.WithCancel(context.Background())
	watcher, err := watch.New(watch.Options{
		Clipboard: mygo.Clipboard, // 天然满足 watch.Clipboard
		Store:     a.st,
		OnCapture: a.onCaptured, // 后台 goroutine → win.Update 投递主线程
		OnError:   func(err error) { log.Printf("历史入库失败: %v", err) },
	})
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	a.watchCancel = cancel // 记下取消函数，供 cleanup 统一释放
	a.watchDone = done
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	return nil
}

// registerHotkey 注册全局快捷键。
//
// 失败不致命：被系统/其他程序占用时仅提示改键，应用内快捷键不受影响。
// mygo.GlobalShortcut 天然满足 hotkey.Backend。
func (a *app) registerHotkey() {
	hotkeys, err := hotkey.New(mygo.GlobalShortcut)
	if err != nil {
		log.Printf("全局快捷键初始化失败: %v", err)
		a.reportStatus("Failed to register global shortcut. Use another shortcut.")
		return
	}
	a.hotkeys = hotkeys
	used, err := hotkey.RegisterWithSettings(hotkeys, a.st, hotkey.ActionToggleWindow, func() {
		a.toggleWindow()
	})
	if err != nil {
		if errors.Is(err, hotkey.ErrInvalidAccelerator) {
			// The saved value was invalid, but RegisterWithSettings has already
			// registered the safe default. Keep the working shortcut and surface
			// a repairable configuration warning instead of reporting failure.
			log.Printf("全局快捷键配置无效，已回退默认键 %s: %v", used, err)
			a.reportStatus("Invalid saved shortcut; using default.")
			return
		}
		// 冲突/配置错误均不致命：记录并提示改键，应用内快捷键不受影响。
		log.Printf("全局快捷键注册失败（可在设置中改键）: %v", err)
		a.reportStatus("Failed to register global shortcut. Use another shortcut.")
		return
	}
	log.Printf("全局快捷键已注册: %s", used)
}

// registerQuitHooks 接好退出流程。
//
// 退出前置标记：mygo 的退出流程会依次关闭各窗口，而窗口的 OnClose
// 默认"隐藏而非关闭"，若不放行会把退出挡掉。OnBeforeQuit 在关窗之前
// 触发（prepareQuit → OnBeforeQuit → w.close），正是打标记的时机。
func (a *app) registerQuitHooks() {
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		a.markQuitting()
	})
	mygo.App.OnQuit(func() {
		a.cleanup()
		applog.Close() // 释放日志文件句柄（Windows 上句柄占用会挡住目录清理）
	})
}

// cleanup 释放已初始化的资源：停止监听、归还快捷键、关闭数据库。
//
// 初始化失败与正常退出共用这一条路径，避免各错误分支各写一遍清理逻辑。
// 可重复调用（幂等）。
func (a *app) cleanup() {
	watchCancel, watchDone := a.watchCancel, a.watchDone
	a.watchCancel = nil
	a.watchDone = nil
	if watchCancel != nil {
		watchCancel() // 停止 watch 轮询（Run 随 ctx 取消退出）
		if watchDone != nil {
			<-watchDone // 关闭数据库前确保监听器不再访问 Store
		}
	}
	if a.hotkeys != nil {
		a.hotkeys.UnregisterAll() // 归还全局快捷键
		a.hotkeys = nil
	}
	a.stopSearchTimer() // 停止搜索防抖计时器（避免退出后仍触发刷新）
	if a.st != nil {
		if err := a.st.Close(); err != nil {
			log.Printf("关闭存储失败: %v", err)
		}
		a.st = nil
	}
}

// reportStatus 在状态栏提示一条信息（经 win.Update 投递主线程）。
func (a *app) reportStatus(msg string) {
	a.win.Update(func() {
		a.status = a.t(msg)
	})
}
