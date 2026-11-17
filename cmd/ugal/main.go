// Command ugal 管理 Steam 已拥有游戏与本地已安装游戏。
package main

import (
	"fmt"
	"os"

	ugalsrc "github.com/HanamiSakiP/u-game"
	"github.com/HanamiSakiP/u-game/internal/cli"
)

func main() {
	// 任意命令首次运行时，把内嵌源码写入 ./.ugal/src，方便离线查看和构建。
	if dir, written, err := ugalsrc.Bootstrap(); err != nil {
		fmt.Fprintf(os.Stderr, "ugal: 写入本地源码失败: %v\n", err)
	} else if written {
		fmt.Fprintf(os.Stderr, "ugal: 已写入本地源码 %s\n", dir)
	}

	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ugal: %v\n", err)
		os.Exit(1)
	}
}
