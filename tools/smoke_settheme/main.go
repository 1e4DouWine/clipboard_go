// smoke_settheme 是一次性冒烟辅助程序：向真实数据目录的主题偏好写入指定模式，
// 用于实机验证"启动即恢复外观偏好"的端到端链路。验证完成后即可删除。
package main

import (
	"fmt"
	"os"

	"my-go-app/store"
	"my-go-app/theme"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: smoke_settheme <dataDir> <system|light|dark>")
		os.Exit(2)
	}
	st, err := store.Open(os.Args[1], store.Options{})
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer st.Close()

	mode := theme.ParseMode(os.Args[2])
	if err := st.SetSetting(theme.SettingsKey, string(mode)); err != nil {
		fmt.Println("set:", err)
		os.Exit(1)
	}
	v, _, _ := st.GetSetting(theme.SettingsKey)
	fmt.Println(theme.SettingsKey, "=", v)
}
