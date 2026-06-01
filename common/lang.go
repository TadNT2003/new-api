package common

import "os"

var BackendLanguage = "en"

func init() {
	lang := os.Getenv("BACKEND_LANGUAGE")
	if lang == "zh-CN" || lang == "zh" {
		BackendLanguage = "zh-CN"
	}
}

// LanguageString picks between English and Chinese based on BackendLanguage.
// Use as the format string when the string contains %v / %s / %d verbs.
func LanguageString(en, zh string) string {
	if BackendLanguage == "zh-CN" {
		return zh
	}
	return en
}
