// Package uiapp 是应用的界面层：把应用状态渲染成 MyGo 视图，并把用户操作
// 翻译成对应用模型的命令。
//
// 分层约定：
//   - Presenter 只持有"渲染所需的只读快照"（Snapshot）与"控件绑定指针"（Config），
//     不引用应用层的结构体，视图与业务互不反向依赖；
//   - 业务状态由应用层持有，视图通过 Actions 回调下发命令；
//   - 控件状态（列表滚动、搜索框焦点）跟随 Presenter，与业务数据分离。
//
// 视图不保存 ui.Element：元素在下一次构建前即失效，跨帧需要持久身份时
// 一律用 ui.Handle / ui.ListState。
package uiapp

import (
	"github.com/egoist/mygo/ui"

	"my-go-app/i18n"
	"my-go-app/store"
)

// State 是每帧渲染所需的只读快照。
type State struct {
	Items   []store.Entry // 当前展示的条目（搜索 + 分类过滤后）
	Chosen  int           // 选中条目索引
	Status  string        // 状态栏提示（错误降级、操作反馈）
	Loading bool          // 列表查询尚未完成
	Locale  i18n.Locale   // 当前界面语言
}

// Config 是与模型字段双向绑定的可写状态。
// 用指针绑定到应用层字段：控件直接写入模型，视图不持有第二份副本。
type Config struct {
	Query        *string // 搜索框绑定值
	Filter       *int    // 分类过滤索引
	ThemeIdx     *int    // 主题切换器绑定值
	LangIdx      *int    // 语言切换器绑定值
	AutoStart    *bool   // 开机自启开关绑定值
	SettingsOpen *bool   // 设置 Modal 开关
}

// Actions 是视图触发的命令回调，全部在主线程执行。
type Actions struct {
	SearchChanged   func()          // 搜索词变化（应用层负责防抖与重载）
	FilterChanged   func()          // 分类过滤变化
	MoveChosen      func(delta int) // 上下移动选中项
	Activate        func(i int)     // 激活（写回剪贴板并快速粘贴）某条目
	DeleteChosen    func()          // 删除选中条目
	ToggleFavorite  func()          // 切换选中条目的收藏
	ToggleAutoStart func(on bool)   // 设置开机自启
	ThemeChanged    func(idx int)   // 切换外观模式
	LangChanged     func(idx int)   // 切换界面语言
	HideWindow      func()          // 隐藏窗口（Esc）
	Quit            func()          // 退出应用
}

// Presenter 承载视图与控件状态。
type Presenter struct {
	// Snapshot 返回当前只读状态快照。视图每帧读取它，保证与模型同步。
	Snapshot func() State
	// Config 是与模型字段绑定的可写状态。
	Config Config
	// Actions 是视图触发的命令回调。
	Actions Actions

	list   ui.ListState // 列表滚动状态（widget 状态，与业务数据分离）
	search ui.Handle    // 搜索框句柄（Ctrl+F 聚焦用）
}

// New 创建 Presenter。Config 和 Actions 必须由应用层一次性完整装配。
func New(snapshot func() State, cfg Config, act Actions) *Presenter {
	return &Presenter{Snapshot: snapshot, Config: cfg, Actions: act}
}

// loc 返回当前界面语言（渲染文案用）。
func (p *Presenter) loc() i18n.Locale { return p.Snapshot().Locale }

// t 返回当前语言下的文案。
func (p *Presenter) t(en string) string { return p.loc().T(en) }

// tpl 返回当前语言下的格式化文案。
func (p *Presenter) tpl(en string, args ...any) string { return p.loc().Tpl(en, args...) }
