package tunnel

import "testing"

func FuzzParseConnString(f *testing.F) {
	f.Add("latch://relay.example.com:7443/?token=lt_a_b")
	f.Add("latch://127.0.0.1/?token=x&ca=&name=n")
	f.Add("not a url")
	f.Add("latch://")
	f.Fuzz(func(t *testing.T, s string) {
		info, err := ParseConnString(s)
		if err == nil && info == nil {
			t.Fatal("nil info without error")
		}
		if err == nil {
			_ = info.String()
		}
	})
}
