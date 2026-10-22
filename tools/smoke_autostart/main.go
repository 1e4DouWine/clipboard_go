// smoke_autostart 是一次性冒烟辅助程序：验证开机自启的注册/查询/注销链路
// （Windows 走 HKCU\...\Run 注册表键）。验证完成后即可删除。
//
// 用法：smoke_autostart on|off
//
//	on  — 注册自启并校验 OpenAtLogin() 为 true
//	off — 注销自启并校验 OpenAtLogin() 为 false
package main

import (
	"fmt"
	"os"

	"github.com/egoist/mygo"
)

func main() {
	mode := "on"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	mygo.App.SetName("Clipboard History")
	mygo.App.WhenReady(func() {
		want := mode == "on"
		if err := mygo.App.SetOpenAtLogin(want); err != nil {
			fmt.Println("SetOpenAtLogin error:", err)
			mygo.App.Exit(1)
			return
		}
		got := mygo.App.OpenAtLogin()
		fmt.Printf("set=%v got=%v\n", want, got)
		if got != want {
			mygo.App.Exit(1)
			return
		}
		mygo.App.Exit(0)
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Println("run:", err)
		os.Exit(1)
	}
}
