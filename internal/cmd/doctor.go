package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"hb/internal/browser"
	"hb/internal/httpx"
	"hb/internal/i18n"
	"hb/internal/store"
)

type doctorTarget struct {
	Name string
	URL  string
}

var doctorTargets = []doctorTarget{
	{"hb.lafi.sh (lafish)", "https://hb.lafi.sh/"},
	{"credit.linux.do (LDC)", "https://credit.linux.do/"},
	{"hb.unsnow.org (Unsnow)", "https://hb.unsnow.org/"},
}

type doctorResult struct {
	Name      string  `json:"name"`
	URL       string  `json:"url"`
	OK        bool    `json:"ok"`
	Status    int     `json:"status,omitempty"`
	LatencyMS int64   `json:"latency_ms,omitempty"`
	SkewSec   float64 `json:"clock_skew_sec,omitempty"`
	Error     string  `json:"error,omitempty"`
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: i18n.T("doctor.short"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			home, err := store.Home()
			if err != nil {
				return err
			}
			c := httpx.New()
			results := make([]doctorResult, 0, len(doctorTargets))
			for _, t := range doctorTargets {
				results = append(results, probe(ctx, c, t))
			}

			type browserRow struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Family  string `json:"family"`
				Exe     string `json:"exe"`
				Default bool   `json:"default"`
			}
			def := browser.DefaultID()
			var browsers []browserRow
			for _, b := range browser.Installed() {
				fam := "chromium"
				if b.Kind.Family == browser.Gecko {
					fam = "gecko"
				}
				browsers = append(browsers, browserRow{b.Kind.ID, b.Kind.Name, fam, b.Exe, b.Kind.ID == def})
			}

			out := cmd.OutOrStdout()
			if flagJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"home": home, "browsers": browsers, "targets": results})
			}
			fmt.Fprintln(out, i18n.T("doctor.config", home))
			for _, b := range browsers {
				tag := ""
				if b.Default {
					tag = i18n.T("login.default.tag")
				}
				fmt.Fprintf(out, "%-18s %-8s %s%s\n", b.Name, b.Family, b.Exe, tag)
			}
			for _, r := range results {
				if !r.OK {
					fmt.Fprintln(out, i18n.T("doctor.reach.fail", r.Name, r.Error))
					continue
				}
				fmt.Fprintln(out, i18n.T("doctor.reach.ok", r.Name, r.LatencyMS, r.Status))
				if r.SkewSec > 60 || r.SkewSec < -60 {
					fmt.Fprintln(out, i18n.T("doctor.skew", r.Name, time.Duration(r.SkewSec*float64(time.Second))))
					fmt.Fprintln(out, i18n.T("doctor.skew.warn"))
				}
			}
			return nil
		},
	}
}

func probe(ctx context.Context, c *httpx.Client, t doctorTarget) doctorResult {
	r := doctorResult{Name: t.Name, URL: t.URL}
	req, err := http.NewRequest(http.MethodHead, t.URL, nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	start := time.Now()
	resp, err := c.Do(ctx, req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer resp.Body.Close()
	r.OK = true
	r.Status = resp.StatusCode
	r.LatencyMS = time.Since(start).Milliseconds()
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		r.SkewSec = time.Since(d).Seconds()
	}
	return r
}
