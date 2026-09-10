package httpx

import (
	"strconv"
	"strings"
)

const (
	uiLocaleEnglish = "en"
	uiLocaleChinese = "zh-CN"
)

func preferredUILocale(header string) string {
	bestLocale := uiLocaleEnglish
	bestQuality := -1.0

	for _, raw := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(raw), ";")
		locale, supported := supportedUILocale(parts[0])
		if !supported {
			continue
		}

		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
			break
		}
		if quality <= 0 || quality <= bestQuality {
			continue
		}
		bestLocale = locale
		bestQuality = quality
	}

	return bestLocale
}

func supportedUILocale(value string) (string, bool) {
	locale := strings.ToLower(strings.TrimSpace(value))
	if locale == "en" || strings.HasPrefix(locale, "en-") {
		return uiLocaleEnglish, true
	}
	if locale == "zh" || locale == "zh-cn" || locale == "zh-sg" || locale == "zh-hans" || strings.HasPrefix(locale, "zh-hans-") {
		return uiLocaleChinese, true
	}
	return "", false
}
