package config

import (
	"testing"
)

func TestSignupResolution(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	cases := []struct {
		signup, invite, want string
		err                  bool
	}{
		{"", "", "first", false},
		{"", "code", "invite", false},
		{"open", "", "open", false},
		{"OPEN", "code", "open", false},
		{"first", "", "first", false},
		{"closed", "", "closed", false},
		{"invite", "code", "invite", false},
		{"invite", "", "", true},
		{"maybe", "", "", true},
	}
	for _, c := range cases {
		t.Setenv("FINALECHAT_SIGNUP", c.signup)
		t.Setenv("FINALECHAT_INVITE_CODE", c.invite)
		cfg, err := Load("test")
		if c.err {
			if err == nil {
				t.Errorf("signup=%q invite=%q: expected an error", c.signup, c.invite)
			}
			continue
		}
		if err != nil {
			t.Errorf("signup=%q invite=%q: %v", c.signup, c.invite, err)
			continue
		}
		if cfg.Signup != c.want {
			t.Errorf("signup=%q invite=%q: got %q want %q", c.signup, c.invite, cfg.Signup, c.want)
		}
	}
}

func TestAttachmentQuotaParsing(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	cases := map[string]int64{
		"":        5 << 30,
		"0":       0,
		"1024":    1024,
		"5GiB":    5 << 30,
		"500MB":   500 << 20,
		"2G":      2 << 30,
		"1.5 GiB": 3 << 29,
		"64KiB":   64 << 10,
	}
	for in, want := range cases {
		t.Setenv("FINALECHAT_ATTACHMENT_QUOTA_BYTES", in)
		cfg, err := Load("test")
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if cfg.AttachmentQuotaBytes != want {
			t.Errorf("%q: got %d want %d", in, cfg.AttachmentQuotaBytes, want)
		}
	}
	for _, in := range []string{"lots", "5XB", "-1"} {
		t.Setenv("FINALECHAT_ATTACHMENT_QUOTA_BYTES", in)
		if _, err := Load("test"); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}
