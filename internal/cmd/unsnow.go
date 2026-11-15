package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hb/internal/auth"
	"hb/internal/provider"
	"hb/internal/provider/unsnow"
)

// hb unsnow: hb.unsnow.org through its official read-only CLI API
// (https://hb.unsnow.org/openapi.json). Each command is one request the
// person asked for; nothing is retried, polled or claimed.

func newUnsnowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "unsnow",
		Short: "hb.unsnow.org 红包（官方只读接口：查口令、广场、我的记录；领取在网页上）",
		Long: "用 hb.unsnow.org 官方的 CLI 只读接口。先在网页「我的 → 认证令牌」创建令牌，再运行 `hb login unsnow`。\n" +
			"只做查询：不领取、不抽奖、不选刮刮乐格子。要领就在浏览器里打开链接，自己过人机验证。\n" +
			"每条命令只发一次请求，失败了不会自动重试。别拿它猜口令：查不存在的口令会累计错误，\n" +
			"连续 10 次要在网页上完成验证，网页和这里共用同一个计数。",
	}
	var asText, asCode bool
	check := &cobra.Command{
		Use:   "check <口令|链接|红包编号>",
		Short: "查一个红包现在能不能领（不会领取）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var force *unsnow.RefKind
			switch {
			case asText && asCode:
				return errors.New("--text 和 --code 只能选一个")
			case asText:
				k := unsnow.RefTextCode
				force = &k
			case asCode:
				k := unsnow.RefCode
				force = &k
			}
			ref, err := unsnow.ParseRef(args[0], force)
			if err != nil {
				return err
			}
			u, s, err := openUnsnow()
			if err != nil {
				return err
			}
			pk, err := u.Lookup(cmd.Context(), s, ref)
			if err != nil {
				return lookupHint(err)
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), pk)
			}
			writePacket(cmd.OutOrStdout(), pk)
			return nil
		},
	}
	check.Flags().BoolVar(&asText, "text", false, "把它当文字口令查（比如 8 位纯数字的文字口令）")
	check.Flags().BoolVar(&asCode, "code", false, "把它当随机口令查")

	var page unsnow.Page
	plaza := &cobra.Command{
		Use:   "plaza",
		Short: "看公开红包广场（一次请求）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u, s, err := openUnsnow()
			if err != nil {
				return err
			}
			pl, err := u.Plaza(cmd.Context(), s, page)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return writeJSON(out, pl)
			}
			if len(pl.List) == 0 {
				fmt.Fprintln(out, "广场上现在没有红包")
				return nil
			}
			for _, pk := range pl.List {
				mark, note := "✗", "领不了"
				if pk.CanClaim {
					mark, note = "✓", "可以领"
				} else if pk.Reason != nil && *pk.Reason != "" {
					note = *pk.Reason
				} else if !pk.Available {
					note = "已结束"
				}
				fmt.Fprintf(out, "%s %s · %s · %s · 总额 %s · %s\n    %s\n",
					mark, pk.Creator.Username, typeName(pk.Type), progressText(pk.Progress), ldc(pk.TotalAmount), note, pk.Link)
			}
			moreLine(out, page, len(pl.List), pl.Total, pl.HasMore)
			return nil
		},
	}
	pageFlags(plaza, &page)

	me := &cobra.Command{
		Use:   "me",
		Short: "令牌属于谁，以及口令查询的错误次数和验证状态",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u, s, err := openUnsnow()
			if err != nil {
				return err
			}
			m, err := u.GetMe(cmd.Context(), s)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return writeJSON(out, m)
			}
			level := "未知"
			if m.User.TrustLevel != nil {
				level = fmt.Sprint(*m.User.TrustLevel)
			}
			fmt.Fprintf(out, "%s（LINUX DO %d，信任等级 %s）· 权限 %s\n", m.User.Username, m.User.LinuxDoID, level, m.Scope)
			cs := m.ClaimSecurity
			fmt.Fprintf(out, "口令连续查错 %d 次（第 %d 次要网页验证）\n", cs.ConsecutiveMisses, cs.Threshold)
			if cs.VerificationRequired {
				url := m.VerificationURL
				if url == "" {
					url = unsnow.VerificationURL
				}
				fmt.Fprintf(out, "需要先完成查询验证：用这个账号在浏览器打开 %s\n", url)
			}
			return nil
		},
	}

	var sentPage, receivedPage unsnow.Page
	sent := &cobra.Command{
		Use:   "sent",
		Short: "我发出的红包（总额和已到账的金额、份数）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u, s, err := openUnsnow()
			if err != nil {
				return err
			}
			h, err := u.Sent(cmd.Context(), s, sentPage)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return writeJSON(out, h)
			}
			if len(h.List) == 0 {
				fmt.Fprintln(out, "还没有发出过红包")
				return nil
			}
			for _, r := range h.List {
				fmt.Fprintf(out, "%s · %s · %s · 已到账 %s / 总额 %s · %d/%d 份 · %s\n    %s\n",
					when(r.CreatedAt), typeName(r.Type), statusName(r.Status), ldc(r.SentAmount), ldc(r.TotalAmount),
					r.SentCount, r.Count, wishText(r.Wish), r.Link)
			}
			moreLine(out, sentPage, len(h.List), h.Total, h.HasMore)
			return nil
		},
	}
	pageFlags(sent, &sentPage)
	received := &cobra.Command{
		Use:   "received",
		Short: "我领到并已到账的红包（不含待核对、失败或未中奖的）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u, s, err := openUnsnow()
			if err != nil {
				return err
			}
			h, err := u.Received(cmd.Context(), s, receivedPage)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return writeJSON(out, h)
			}
			if len(h.List) == 0 {
				fmt.Fprintln(out, "还没有到账的领取记录")
				return nil
			}
			for _, r := range h.List {
				fmt.Fprintf(out, "%s · %s · 来自 %s · %s · %s\n    %s\n",
					when(r.ReceivedAt), ldc(r.ReceivedAmount), r.CreatorUsername, typeName(r.Type), wishText(r.Wish), r.Link)
			}
			moreLine(out, receivedPage, len(h.List), h.Total, h.HasMore)
			return nil
		},
	}
	pageFlags(received, &receivedPage)

	c.AddCommand(me, check, plaza, sent, received)
	return c
}

func openUnsnow() (*unsnow.Unsnow, auth.Session, error) {
	prof, err := profileName()
	if err != nil {
		return nil, auth.Session{}, err
	}
	s, err := auth.Load(prof, "unsnow")
	if err != nil {
		return nil, auth.Session{}, err
	}
	if s.Token == "" {
		return nil, auth.Session{}, errors.New("保存的是旧的 cookie 登录；Unsnow 现在用官方令牌：在网页「我的 → 认证令牌」创建后运行 `hb login unsnow`")
	}
	p, err := provider.Get("unsnow")
	if err != nil {
		return nil, auth.Session{}, err
	}
	u, ok := p.(*unsnow.Unsnow)
	if !ok {
		return nil, auth.Session{}, errors.New("unsnow provider unavailable")
	}
	return u, s, nil
}

// lookupHint adds what a passcode miss means for the account.
func lookupHint(err error) error {
	var e *unsnow.APIError
	if errors.As(err, &e) && e.LookupCounted && e.Code != "lookup_captcha_required" {
		return fmt.Errorf("%w（这次查询记了一次错误口令，`hb unsnow me` 可以看累计次数）", err)
	}
	return err
}

func writePacket(out io.Writer, pk unsnow.Packet) {
	name := pk.Creator.Username
	fmt.Fprintf(out, "%s 的%s红包 · %s · 总额 %s · %s\n", name, typeName(pk.Type), progressText(pk.Progress), ldc(pk.TotalAmount), statusName(pk.Status))
	if pk.Wish != nil && *pk.Wish != "" {
		fmt.Fprintf(out, "祝福：%s\n", *pk.Wish)
	}
	if len(pk.Levels) > 0 && len(pk.Levels) < 5 {
		lv := make([]string, len(pk.Levels))
		for i, l := range pk.Levels {
			lv[i] = fmt.Sprint(l)
		}
		fmt.Fprintf(out, "信任等级限制：%s\n", strings.Join(lv, "/"))
	}
	if pk.MaxClaims24h != nil {
		fmt.Fprintf(out, "最近 24 小时领过 %d 次的人不能再领\n", *pk.MaxClaims24h)
	}
	if pk.ExpiresAt != nil {
		fmt.Fprintf(out, "%s 过期\n", when(*pk.ExpiresAt))
	}
	if pk.TextCodeLocked {
		until := ""
		if pk.TextCodeLockedUntil != nil {
			until = "，" + when(*pk.TextCodeLockedUntil) + " 前"
		}
		fmt.Fprintf(out, "这个文字口令本轮你已经领过%s不能再领同口令的红包\n", until)
	}
	switch {
	case pk.CanClaim:
		fmt.Fprintln(out, "现在可以领（以领取时为准）→ 在浏览器里打开下面的链接，自己过人机验证并点领取：")
	case pk.Reason != nil && *pk.Reason != "":
		fmt.Fprintf(out, "现在领不了：%s\n", *pk.Reason)
	case !pk.Available:
		fmt.Fprintln(out, "现在领不了：已结束")
	default:
		fmt.Fprintln(out, "现在领不了")
	}
	fmt.Fprintln(out, pk.Link)
}

func pageFlags(c *cobra.Command, p *unsnow.Page) {
	c.Flags().IntVar(&p.Limit, "limit", 0, "每页几项（1–50，默认 20）")
	c.Flags().IntVar(&p.Offset, "offset", 0, "从第几项开始（0–10000）")
}

func moreLine(out io.Writer, p unsnow.Page, shown, total int, more bool) {
	if more {
		fmt.Fprintf(out, "共 %d 项，下一页：--offset %d\n", total, p.Offset+shown)
	}
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

var unsnowTypes = map[string]string{
	"random": "拼手气", "equal": "均分", "exclusive": "专属", "lottery": "抽奖", "scratch": "刮刮乐",
}

func typeName(t string) string {
	if n, ok := unsnowTypes[t]; ok {
		return n
	}
	return t
}

func statusName(s string) string {
	switch s {
	case "active":
		return "进行中"
	case "ended":
		return "已结束"
	case "expired":
		return "已过期"
	}
	return s
}

func progressText(p unsnow.Progress) string {
	if p.Kind == "cells" { // a 刮刮乐 whose results are hidden: cells picked, not winners
		return fmt.Sprintf("已选 %d/%d 格", p.Used, p.Total)
	}
	return fmt.Sprintf("%d/%d 份", p.Used, p.Total)
}

func ldc(v float64) string { return fmt.Sprintf("%.2f LDC", v) }

func when(ms int64) string { return time.UnixMilli(ms).Local().Format("01-02 15:04") }

func wishText(w *string) string {
	if w == nil || *w == "" {
		return "（无祝福）"
	}
	return *w
}
