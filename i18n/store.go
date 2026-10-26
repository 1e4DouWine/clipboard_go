package i18n

// Settings 是语言偏好持久化所需的最小能力。
//
// 抽象成接口的目的：i18n 不必依赖具体的存储实现（*store.Store 天然满足），
// 便于单测注入内存实现。
type Settings interface {
	// GetSetting 读取配置项；第二个返回值表示 key 是否存在。
	GetSetting(key string) (string, bool, error)
	// SetSetting 写入/更新配置项。
	SetSetting(key, value string) error
}

// Load 从设置存储读取语言偏好。
// 未设置、读取失败或非法值 → LangEN（默认英文）。
func Load(s Settings) Lang {
	v, ok, err := s.GetSetting(SettingsKey)
	if err != nil || !ok {
		return LangEN
	}
	return ParseLang(v)
}

// Save 持久化语言偏好（存 "en" / "zh"）。
func Save(s Settings, l Lang) error {
	v := "en"
	if normalize(l) == LangZH {
		v = "zh"
	}
	return s.SetSetting(SettingsKey, v)
}
