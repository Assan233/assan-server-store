package auth

import (
	"unicode"
	"unicode/utf8"
)

// PasswordRequirement describes the policy used whenever a password is set.
const PasswordRequirement = "密码至少 8 位，须包含大写字母、小写字母、数字和特殊字符，且不超过 128 字节"

func validPassword(password string) bool {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 8 || len(password) > 128 {
		return false
	}
	var upper, lower, digit, special bool
	for _, c := range password {
		upper = upper || c >= 'A' && c <= 'Z'
		lower = lower || c >= 'a' && c <= 'z'
		digit = digit || c >= '0' && c <= '9'
		special = special || unicode.IsPunct(c) || unicode.IsSymbol(c)
	}
	return upper && lower && digit && special
}
