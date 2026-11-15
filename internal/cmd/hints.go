package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"hb/internal/game/kddb"
	"hb/internal/store"
)

func newHintsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "hints",
		Short: "矿洞夺宝建议功能的校准报告（检验它准不准）",
	}
	var file string
	report := &cobra.Command{
		Use:   "report",
		Short: "读 --hints-log 记下的日志，看建议准不准",
		Long: `用 --hints-log 玩的时候，你每手动挖一格，hb 就把"挖之前建议对这格的预测"
和"挖出来实际是什么"记到本地日志。这条命令读日志：

  · 预测"零风险"的格子里有没有挖出危险（应该一次都没有）；
  · 其余格子按预测的危险概率分档，对比实际踩到危险的比例。

日志只在本机，是 JSON 行，可以直接打开看或删掉。`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := file
			if path == "" {
				home, err := store.Home()
				if err != nil {
					return err
				}
				path = filepath.Join(home, "hints-log.jsonl")
			}
			f, err := os.Open(path)
			if os.IsNotExist(err) {
				fmt.Fprintf(cmd.OutOrStdout(), "还没有日志（%s）：用 --hints-log 手动挖几格再来看\n", path)
				return nil
			}
			if err != nil {
				return err
			}
			defer f.Close()
			cal, err := kddb.Calibrate(f)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), cal.String())
			return nil
		},
	}
	report.Flags().StringVar(&file, "file", "", "log file (default: hints-log.jsonl in hb's config dir)")
	root.AddCommand(report)
	return root
}
