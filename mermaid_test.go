package stateless_test

import (
	"bytes"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/qmuntal/stateless"
)

func TestStateMachine_ToMermaid(t *testing.T) {
	tests := []func() *stateless.StateMachine{
		emptyWithInitial,
		withSubstate,
		withInitialState,
		withGuards,
		withUnicodeNames,
		phoneCall,
	}
	if err := os.MkdirAll("testdata/mermaid", 0755); err != nil {
		t.Fatal(err)
	}
	for _, fn := range tests {
		name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
		sp := strings.Split(name, ".")
		name = sp[len(sp)-1]
		t.Run(name, func(t *testing.T) {
			got := fn().ToMermaid()
			fname := "testdata/mermaid/" + name + ".mmd"
			want, err := os.ReadFile(fname)
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if *update {
				if !bytes.Equal([]byte(got), want) {
					if werr := os.WriteFile(fname, []byte(got), 0666); werr != nil {
						t.Fatal(werr)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal([]byte(got), want) {
					t.Fatalf("got:\n%swant:\n%s", got, want)
				}
			}
		})
	}
}

func BenchmarkToMermaid(b *testing.B) {
	sm := phoneCall()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sm.ToMermaid()
	}
}
