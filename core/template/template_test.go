package template_test

import (
	"strings"
	"testing"

	"github.com/huge-zhang/zdog-template/core/template"
	"github.com/huge-zhang/zdog-template/core/template/model"
)

func load(t *testing.T, src string) *template.Template {
	t.Helper()
	tpl, err := template.Load(src)
	if err != nil {
		t.Fatalf("Load(%q) error: %v", src, err)
	}
	return tpl
}

func tags(tpl *template.Template) []model.Tag {
	out := make([]model.Tag, 0, len(tpl.Elements()))
	for _, e := range tpl.Elements() {
		out = append(out, e.Tag())
	}
	return out
}

func names(tpl *template.Template) []string {
	var out []string
	for _, e := range tpl.Elements() {
		if n := e.Name(); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func TestLoadShape(t *testing.T) {
	tpl := load(t, "[${ts}] ${lv} ${msg} payload=${json|name=p}")
	wantTags := []model.Tag{
		model.TagText, model.TagRegex, model.TagText, model.TagRegex,
		model.TagText, model.TagRegex, model.TagText, model.TagJSON,
	}
	got := tags(tpl)
	if len(got) != len(wantTags) {
		t.Fatalf("got %d elements %v, want %d", len(got), got, len(wantTags))
	}
	for i := range wantTags {
		if got[i] != wantTags[i] {
			t.Errorf("element %d: tag = %q, want %q", i, got[i], wantTags[i])
		}
	}
	wantNames := []string{"ts", "lv", "msg", "p"}
	gotNames := names(tpl)
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Errorf("names = %v, want %v", gotNames, wantNames)
			break
		}
	}
}

func TestLoadSugarEqualsExplicit(t *testing.T) {
	a := load(t, "${ts}")
	b := load(t, "${re|name=ts}")
	if a.Dump() != b.Dump() {
		t.Errorf("sugar %q != explicit %q", a.Dump(), b.Dump())
	}
}

func TestAnonymousHolesGetNames(t *testing.T) {
	tpl := load(t, `${re|expr=\d+}-${re|expr=\w+}`)
	got := names(tpl)
	if len(got) != 2 || got[0] == "" || got[1] == "" || got[0] == got[1] {
		t.Errorf("anonymous holes not uniquely named: %v", got)
	}
}

func TestDuplicateNameRejected(t *testing.T) {
	_, err := template.Load("${a}-${a}")
	if err == nil || !strings.Contains(err.Error(), "duplicate binding name") {
		t.Errorf("expected duplicate name error, got %v", err)
	}
}

func TestLoadErrors(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"${nope|name=x}", "unsupported tag"},
		{"${ext|name=x}", "requires an extension"},
		{"${ext|extension=missing}", "unknown extension"},
		{"${text|name=x}", "requires a content"},
		{`${re|expr=[bad}`, "invalid expr"},
		{"abc${unterminated", "unterminated"},
		{"${re|name=x,greedy=maybe}", "not a boolean"},
	} {
		_, err := template.Load(tc.src)
		if err == nil {
			t.Errorf("Load(%q): expected error", tc.src)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Load(%q): error = %q, want substring %q", tc.src, err, tc.want)
		}
	}
}

// Load(Dump(t)) 必须与 t 等价 —— 模板本身也要满足往返。
func TestDumpReloadable(t *testing.T) {
	for _, src := range []string{
		"[${ts}] ${lv} ${msg} payload=${json|name=p}",
		"plain text only",
		"${a}${b}",
		`${re|name=n,expr=\d{1\,3}}`,
		`literal \$dollar and ${x}`,
		`${re|name=g,expr=\w+,greedy=true}`,
	} {
		tpl := load(t, src)
		dumped := tpl.Dump()
		again, err := template.Load(dumped)
		if err != nil {
			t.Fatalf("Load(Dump(%q)) = Load(%q) error: %v", src, dumped, err)
		}
		if again.Dump() != dumped {
			t.Errorf("dump not stable:\n src: %q\n 1st: %q\n 2nd: %q", src, dumped, again.Dump())
		}
		if len(again.Elements()) != len(tpl.Elements()) {
			t.Errorf("%q: element count %d != %d after reload",
				src, len(again.Elements()), len(tpl.Elements()))
		}
	}
}

func TestEmptyTemplate(t *testing.T) {
	tpl := load(t, "")
	if len(tpl.Elements()) != 0 {
		t.Errorf("empty template has %d elements", len(tpl.Elements()))
	}
	if tpl.Dump() != "" {
		t.Errorf("empty template dumps to %q", tpl.Dump())
	}
}

func TestExpressAliasAccepted(t *testing.T) {
	// 旧原型用 express= 拼写，保持兼容。
	tpl := load(t, `${re|name=n,express=\d+}`)
	h, ok := tpl.Elements()[0].(model.Hole)
	if !ok {
		t.Fatalf("want Hole, got %T", tpl.Elements()[0])
	}
	if h.Pattern() != `\d+` {
		t.Errorf("pattern = %q, want %q", h.Pattern(), `\d+`)
	}
}
