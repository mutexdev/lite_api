package bru

// What a collection file may be named: the characters Bruno rejects, the reserved device names, and the trimming rules.
//
// Split out by AST: declarations are identified by the parser and copied
// verbatim from their source offsets.

import (
	"regexp"
)

func SanitizeName(name string) string {
	name = brunoInvalidFilenameCharacterPattern.ReplaceAllString(name, "-")
	name = brunoLeadingFilenameTrimPattern.ReplaceAllString(name, "")
	name = brunoTrailingFilenameTrimPattern.ReplaceAllString(name, "")
	return name
}

func ValidateName(name string) bool {
	if name == "" {
		return false
	}
	if len([]rune(name)) > 255 {
		return false
	}
	if brunoReservedDeviceNamePattern.MatchString(name) {
		return false
	}
	return brunoFilenameFirstCharacterPattern.MatchString(name) &&
		brunoFilenameMiddleCharactersPattern.MatchString(name) &&
		brunoFilenameLastCharacterPattern.MatchString(name)
}

var brunoInvalidFilenameCharacterPattern = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)

var invalidPostmanVariableCharacterPattern = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

var brunoFilenameFirstCharacterPattern = regexp.MustCompile(`^[^\s\-<>:"/\\|?*\x00-\x1F]`)

var brunoFilenameLastCharacterPattern = regexp.MustCompile(`[^.\s<>:"/\\|?*\x00-\x1F]$`)

var brunoFilenameMiddleCharactersPattern = regexp.MustCompile(`^[^<>:"/\\|?*\x00-\x1F]*$`)

var brunoLeadingFilenameTrimPattern = regexp.MustCompile(`^[\s-]+`)

var brunoReservedDeviceNamePattern = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$`)

var brunoTrailingFilenameTrimPattern = regexp.MustCompile(`[.\s]+$`)
