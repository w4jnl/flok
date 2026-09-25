//go:build darwin

package awake

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// assertionsOf lists this process's assertion types named Name in `pmset -g assertions`.
func assertionsOf(t *testing.T, name string) []string {
	t.Helper()
	out, err := exec.Command("pmset", "-g", "assertions").Output()
	if err != nil {
		t.Skipf("pmset: %v", err)
	}
	prefix := "pid " + strconv.Itoa(os.Getpid()) + "("
	var types []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) || !strings.Contains(line, `named: "`+name+`"`) {
			continue
		}
		for _, typ := range assertionTypes {
			if strings.Contains(line, " "+typ+" ") {
				types = append(types, typ)
			}
		}
	}
	return types
}

func TestHoldAndRelease(t *testing.T) {
	if !Supported() {
		t.Fatal("darwin must be supported")
	}
	name := Name + " test"
	a, err := Hold(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := assertionsOf(t, name); len(got) != len(assertionTypes) {
		a.Release()
		t.Fatalf("held assertions = %v, want %v", got, assertionTypes)
	}
	a.Release()
	a.Release() // idempotent
	if got := assertionsOf(t, name); len(got) != 0 {
		t.Fatalf("assertions left after Release: %v", got)
	}
	var nilAssertion *Assertion
	nilAssertion.Release()
}
