package main

import (
	"regexp"
	"testing"
)

func TestRandomAliasKeepsSixDigitCompatibilityFormat(t *testing.T) {
	alias, err := randomAlias()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^梧桐#[0-9]{6}$`).MatchString(alias) {
		t.Fatalf("unexpected alias format: %s", alias)
	}
}
