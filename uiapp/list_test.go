package uiapp

import (
	"strings"
	"testing"
	"time"

	"my-go-app/store"
)

func TestMetaLine(t *testing.T) {
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		source, want string
	}{
		{"VS Code", "VS Code · 03-10 12:00"},
		{strings.Repeat("源", maxSourceRunes+1), strings.Repeat("源", maxSourceRunes) + "… · 03-10 12:00"},
		{"  ", "03-10 12:00"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			e := store.Entry{CopiedAt: at.UnixMilli(), SourceApp: tc.source}
			if got := metaLine(e); got != tc.want {
				t.Fatalf("metaLine = %q, want %q", got, tc.want)
			}
		})
	}
}
