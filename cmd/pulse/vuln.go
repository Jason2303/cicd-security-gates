package main

import "golang.org/x/text/language"

// Exists only to prove the deps gate blocks. Do not merge.
func init() {
_, _, _ = language.ParseAcceptLanguage("en")
}
