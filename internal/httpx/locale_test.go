package httpx

import "testing"

func TestPreferredUILocale(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "default english", header: "", want: uiLocaleEnglish},
		{name: "simplified chinese", header: "zh-CN, en;q=0.8", want: uiLocaleChinese},
		{name: "hans chinese", header: "zh-Hans-SG, en;q=0.8", want: uiLocaleChinese},
		{name: "quality prefers english", header: "zh-CN;q=0.5, en-US;q=0.9", want: uiLocaleEnglish},
		{name: "traditional falls through to english", header: "zh-TW, en-US;q=0.8", want: uiLocaleEnglish},
		{name: "unsupported then simplified", header: "fr-FR, zh-SG;q=0.8", want: uiLocaleChinese},
		{name: "zero quality ignored", header: "zh-CN;q=0, en;q=0.7", want: uiLocaleEnglish},
		{name: "invalid quality ignored", header: "zh-CN;q=oops, en;q=0.7", want: uiLocaleEnglish},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := preferredUILocale(test.header); got != test.want {
				t.Fatalf("preferredUILocale(%q)=%q want=%q", test.header, got, test.want)
			}
		})
	}
}
