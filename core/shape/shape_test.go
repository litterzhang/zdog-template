package shape_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/litterzhang/zdog-template/core/shape"
	"github.com/litterzhang/zdog-template/core/shape/model"
	"github.com/litterzhang/zdog-template/core/shape/node"
)

func mustLoad(t *testing.T, s string) *shape.Shape {
	t.Helper()
	sh, err := shape.LoadString(s)
	if err != nil {
		t.Fatalf("LoadString(%s) error: %v", s, err)
	}
	return sh
}

func TestLoadBasicTypes(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want model.Type
	}{
		{`{"type":"string"}`, model.TypeString},
		{`{"type":"number"}`, model.TypeNumber},
		{`{"type":"bool"}`, model.TypeBool},
		{`{"type":"any"}`, model.TypeAny},
	} {
		sh := mustLoad(t, tc.src)
		if got := sh.Node().Type(); got != tc.want {
			t.Errorf("%s: type = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	for _, tc := range []struct{ src, wantSubstr string }{
		{`{}`, "missing type"},
		{`{"type":"int"}`, "unsupported type"},
		{`{"type":123}`, "invalid type"},
		{`{"type":"object"}`, "missing properties"},
		{`{"type":"array"}`, "missing items"},
		{`{"type":"dict"}`, "missing items"},
		{`{"type":"object","properties":{"a":{"type":"nope"}}}`, `property "a"`},
		{`{"type":"array","items":{"type":"nope"}}`, "array items"},
		{`not json`, "invalid json"},
	} {
		_, err := shape.LoadString(tc.src)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Errorf("%s: error = %q, want substring %q", tc.src, err, tc.wantSubstr)
		}
	}
}

// 回归测试：旧原型 dict_node.go 的 Dump 用 := 遮蔽了外层 err，
// items 解析失败时错误被吞掉并返回 nil map。这里确保 dict 与 array 行为一致。
func TestDictDumpPropagatesErrors(t *testing.T) {
	bad := node.NewDict(nil, model.Meta{}) // items 为 nil，Dump 必须报错而不是静默返回
	if _, err := node.Dump(bad); err == nil {
		t.Fatal("dict with nil items: expected Dump error, got nil")
	}
	badArr := node.NewArray(nil, model.Meta{})
	if _, err := node.Dump(badArr); err == nil {
		t.Fatal("array with nil items: expected Dump error, got nil")
	}
}

func TestRoundTripLoadDump(t *testing.T) {
	srcs := []string{
		`{"type":"string"}`,
		`{"type":"any","desc":"anything"}`,
		`{"type":"array","items":{"type":"number"}}`,
		`{"type":"dict","items":{"type":"string"}}`,
		`{"title":"Event","type":"object","properties":{` +
			`"host":{"type":"string"},` +
			`"pct":{"type":"number","format":"%d"},` +
			`"tags":{"type":"array","items":{"type":"string"}},` +
			`"meta":{"type":"dict","items":{"type":"any"}}}}`,
	}
	for _, src := range srcs {
		sh := mustLoad(t, src)
		dumped, err := shape.Dump(sh)
		if err != nil {
			t.Fatalf("Dump(%s) error: %v", src, err)
		}
		// 语义等价比较：Load -> Dump -> Load -> Dump 必须稳定（幂等）
		again := mustLoad(t, string(dumped))
		dumped2, err := shape.Dump(again)
		if err != nil {
			t.Fatalf("second Dump error: %v", err)
		}
		var a, b any
		_ = json.Unmarshal(dumped, &a)
		_ = json.Unmarshal(dumped2, &b)
		if !jsonEqual(a, b) {
			t.Errorf("round trip not idempotent\n src:  %s\n 1st:  %s\n 2nd:  %s", src, dumped, dumped2)
		}
	}
}

func TestMetaPreserved(t *testing.T) {
	sh := mustLoad(t, `{"type":"number","desc":"cpu","format":"%.2f","nullable":true,"required":true,"default":0}`)
	m := sh.Node().Meta()
	if m.Desc != "cpu" || m.Format != "%.2f" || !m.Nullable || !m.Required {
		t.Errorf("meta not preserved: %+v", m)
	}
	if m.Default == nil {
		t.Error("default not preserved")
	}
	out, err := shape.Dump(sh)
	if err != nil {
		t.Fatalf("Dump error: %v", err)
	}
	for _, k := range []string{"desc", "format", "nullable", "required", "default"} {
		if !strings.Contains(string(out), `"`+k+`"`) {
			t.Errorf("dumped shape missing %q: %s", k, out)
		}
	}
}

func TestObjectKeysSorted(t *testing.T) {
	sh := mustLoad(t, `{"type":"object","properties":{"c":{"type":"any"},"a":{"type":"any"},"b":{"type":"any"}}}`)
	obj, ok := sh.Node().(*node.Object)
	if !ok {
		t.Fatalf("want *node.Object, got %T", sh.Node())
	}
	got := obj.Keys()
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Keys() = %v, want %v", got, want)
		}
	}
}

func TestTitleRoundTrip(t *testing.T) {
	sh := mustLoad(t, `{"title":"Foo","type":"string"}`)
	if sh.Title() != "Foo" {
		t.Errorf("Title() = %q, want Foo", sh.Title())
	}
	out, _ := shape.Dump(sh)
	if !strings.Contains(string(out), `"title":"Foo"`) {
		t.Errorf("title lost: %s", out)
	}
}

func TestNilSafety(t *testing.T) {
	if _, err := shape.Dump(nil); err == nil {
		t.Error("Dump(nil): expected error")
	}
	if _, err := node.Dump(nil); err == nil {
		t.Error("node.Dump(nil): expected error")
	}
	if _, err := node.Parse("not a map"); err == nil {
		t.Error("node.Parse(string): expected error")
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
