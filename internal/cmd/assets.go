package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hb/internal/assets"
	"hb/internal/ui"
)

func wikiBytes() int64 {
	var n int64
	for _, f := range assets.WikiFiles {
		n += f.Bytes
	}
	return n
}

func newAssetsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "assets",
		Short: "管理矿洞夺宝终端版用的 Minecraft 原版图标（现场从 Mojang 官方和 Minecraft Wiki 取，不随 hb 分发）",
		Long: `矿洞夺宝的图标用 Minecraft 原版图标。这些文件是 Mojang / Microsoft 的，
不在 hb 的仓库和发行包里，做法同 kddb：你同意后，hb 从两处取：
  1) Mojang 官方地址下载固定版本（Java ` + assets.Version + `）的客户端 JAR，核对 SHA-1，只取出需要的 ` + fmt.Sprint(len(assets.Files)) + ` 个
     贴图并逐个核对 SHA-256，取完删除 JAR；
  2) Minecraft Wiki 的 ` + fmt.Sprint(len(assets.WikiFiles)) + ` 张背包图标（TNT、盾牌、末影箱、宝库、刷怪笼、藏宝图、远古残骸、
     附魔金苹果），核对 SHA-1 和 SHA-256。
图标只在你本机显示用；没有时 hb 用内置的简易图标。`,
	}

	var yes bool
	fetch := &cobra.Command{
		Use:   "fetch",
		Short: "下载并核对贴图（约 28 MB 的一次性下载，取完删 JAR）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := assets.Dir()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(assets.Check(dir, assets.AllFiles())) == 0 {
				u := ui.For(out)
				fmt.Fprintln(out, u.Mark(true)+" 素材已齐 "+u.Dim(dir))
				return nil
			}
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(), "将从两处下载：\n"+
					"  1) Mojang 官方：Minecraft %s 客户端 JAR（约 %.0f MB）\n     %s\n     只取出 %d 个贴图（共几 KB），取完删除 JAR；\n"+
					"  2) Minecraft Wiki（minecraft.wiki）：%d 张背包图标（约 %.0f KB）。\n"+
					"都放在 %s，逐个核对散列。\n这些素材归 Mojang / Microsoft 所有，仅供你在本机显示用，hb 不分发。\n",
					assets.Version, float64(assets.ClientBytes)/1e6, assets.ClientURL, len(assets.Files),
					len(assets.WikiFiles), float64(wikiBytes())/1024, dir)
				ans, err := inputText("继续下载吗？输入 y 确认", "", false)
				if err != nil {
					return fmt.Errorf("%w（或用 --yes 表示同意）", err)
				}
				if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
					fmt.Fprintln(out, "没有下载。")
					return nil
				}
			}
			return assets.Fetch(cmd.Context(), dir, func(s string) { fmt.Fprintln(cmd.ErrOrStderr(), s) })
		},
	}
	fetch.Flags().BoolVarP(&yes, "yes", "y", false, "同意从 Mojang 官方地址下载（不再询问）")

	check := &cobra.Command{
		Use:   "check",
		Short: "核对已有的贴图（缺了或不对会列出来，退出码 1）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := assets.Dir()
			if err != nil {
				return err
			}
			bad := assets.Check(dir, assets.AllFiles())
			if len(bad) == 0 {
				u := ui.For(cmd.OutOrStdout())
				fmt.Fprintf(cmd.OutOrStdout(), "%s 素材齐全，%d 个文件都对 %s\n", u.Mark(true), len(assets.AllFiles()), u.Dim(dir))
				return nil
			}
			u := ui.For(cmd.OutOrStdout())
			for _, f := range bad {
				fmt.Fprintln(cmd.OutOrStdout(), u.Mark(false)+" "+u.Bad("缺或不对")+" "+f.Path)
			}
			return fmt.Errorf("缺 %d 个，运行 hb assets fetch", len(bad))
		},
	}

	clean := &cobra.Command{
		Use:   "clean",
		Short: "删除已下载的贴图",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := assets.Dir()
			if err != nil {
				return err
			}
			if err := assets.Remove(dir); err != nil {
				return err
			}
			u := ui.For(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), u.Mark(true)+" 已删除 "+u.Dim(dir))
			return nil
		},
	}
	root.AddCommand(fetch, check, clean)
	return root
}
