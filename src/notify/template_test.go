package notify

import (
	"strings"
	"testing"
)

func TestParseTemplate(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantSubject string
		wantBody    string
		wantErr     bool
	}{
		{
			name:        "subject body separated",
			raw:         "Subject: Subject line\n---\nBody line\n",
			wantSubject: "Subject line",
			wantBody:    "Body line\n",
		},
		{
			name:        "leading BOM is stripped",
			raw:         "\ufeffSubject: S\n---\nBody\n",
			wantSubject: "S",
			wantBody:    "Body\n",
		},
		{
			name:        "CRLF line endings are normalized",
			raw:         "Subject: S\r\n---\r\nBody\r\n",
			wantSubject: "S",
			wantBody:    "Body\n",
		},
		{
			name:        "multi-line body keeps its blank lines",
			raw:         "Subject: S\n---\nfirst\n\nsecond\n",
			wantSubject: "S",
			wantBody:    "first\n\nsecond\n",
		},
		{
			name:    "missing subject prefix",
			raw:     "Only a subject\n---\nbody\n",
			wantErr: true,
		},
		{
			name:    "missing separator",
			raw:     "Subject: S\nbody\n",
			wantErr: true,
		},
		{
			name:    "empty source",
			raw:     "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTemplate("test", tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTemplate(%q) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTemplate(%q): %v", tc.raw, err)
			}
			if got.Subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", got.Subject, tc.wantSubject)
			}
			if got.Body != tc.wantBody {
				t.Errorf("body = %q, want %q", got.Body, tc.wantBody)
			}
		})
	}
}

func TestTemplateRender(t *testing.T) {
	tests := []struct {
		name        string
		tmpl        Template
		vars        map[string]string
		wantSubject string
		wantBody    string
	}{
		{
			name:        "substitutes known variables",
			tmpl:        Template{Subject: "{app_name} alert", Body: "from {fqdn}"},
			vars:        map[string]string{"app_name": "shortner", "fqdn": "example.com"},
			wantSubject: "shortner alert",
			wantBody:    "from example.com",
		},
		{
			name:        "missing value renders empty, never the raw token",
			tmpl:        Template{Subject: "a{onion_url}b", Body: "b"},
			vars:        map[string]string{"app_name": "x"},
			wantSubject: "ab",
			wantBody:    "b",
		},
		{
			name:        "malformed placeholder is left verbatim",
			tmpl:        Template{Subject: "{not a var} {unclosed", Body: "{UPPER}"},
			vars:        map[string]string{},
			wantSubject: "{not a var} {unclosed",
			wantBody:    "{UPPER}",
		},
		{
			name:        "repeated placeholder replaced everywhere",
			tmpl:        Template{Subject: "{ip} {ip}", Body: "{ip}"},
			vars:        map[string]string{"ip": "10.0.0.1"},
			wantSubject: "10.0.0.1 10.0.0.1",
			wantBody:    "10.0.0.1",
		},
		{
			name:        "nil vars renders every placeholder empty",
			tmpl:        Template{Subject: "s", Body: "[{fqdn}]"},
			vars:        nil,
			wantSubject: "s",
			wantBody:    "[]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, body := tc.tmpl.Render(tc.vars)
			if subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestTemplatePlaceholders(t *testing.T) {
	tests := []struct {
		name string
		tmpl Template
		want []string
	}{
		{
			name: "subject and body are both scanned and sorted",
			tmpl: Template{Subject: "{b}", Body: "{a}"},
			want: []string{"a", "b"},
		},
		{
			name: "duplicates collapse",
			tmpl: Template{Subject: "{a} {a}", Body: "{a}"},
			want: []string{"a"},
		},
		{
			name: "malformed tokens are not placeholders",
			tmpl: Template{Subject: "{not a var}", Body: "plain"},
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tmpl.Placeholders()
			if len(got) != len(tc.want) {
				t.Fatalf("Placeholders() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Placeholders() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestValidateTemplate(t *testing.T) {
	known := map[string]bool{"fqdn": true, "app_name": true}

	tests := []struct {
		name        string
		tmpl        Template
		wantErrs    int
		errContains string
		wantWarn    bool
	}{
		{
			name:     "clean template",
			tmpl:     Template{Name: "t", Subject: "{app_name}", Body: "{fqdn}"},
			wantErrs: 0,
		},
		{
			name:        "empty subject",
			tmpl:        Template{Name: "t", Subject: "", Body: "b"},
			wantErrs:    1,
			errContains: "Subject cannot be empty",
		},
		{
			name:        "empty body",
			tmpl:        Template{Name: "t", Subject: "s", Body: ""},
			wantErrs:    1,
			errContains: "Body cannot be empty",
		},
		{
			name:        "unknown variable suggests the closest known one",
			tmpl:        Template{Name: "t", Subject: "{fqdnn}", Body: "b"},
			wantErrs:    1,
			errContains: "Did you mean {fqdn}?",
		},
		{
			name:        "unrecognizable variable has no suggestion",
			tmpl:        Template{Name: "t", Subject: "{zzzzzzzzz}", Body: "b"},
			wantErrs:    1,
			errContains: "Unknown variable: {zzzzzzzzz}",
		},
		{
			name:     "over-long subject warns without failing",
			tmpl:     Template{Name: "t", Subject: strings.Repeat("x", maxSubjectLength+1), Body: "b"},
			wantErrs: 0,
			wantWarn: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateTemplate(tc.tmpl, known)
			if len(v.Errors) != tc.wantErrs {
				t.Fatalf("Errors = %v, want %d", v.Errors, tc.wantErrs)
			}
			if tc.errContains != "" && !strings.Contains(strings.Join(v.Errors, "|"), tc.errContains) {
				t.Errorf("Errors = %v, want one containing %q", v.Errors, tc.errContains)
			}
			if v.OK() != (tc.wantErrs == 0) {
				t.Errorf("OK() = %v, want %v", v.OK(), tc.wantErrs == 0)
			}
			if got := len(v.Warnings) > 0; got != tc.wantWarn {
				t.Errorf("warnings present = %v, want %v (%v)", got, tc.wantWarn, v.Warnings)
			}
		})
	}
}

func TestValidateRaw(t *testing.T) {
	tests := []struct {
		name     string
		tmplName string
		raw      string
		wantOK   bool
	}{
		{name: "valid known event", tmplName: EventSecurityAlert, raw: "Subject: {event}\n---\n{ip} {details}\n", wantOK: true},
		{name: "unparseable source", tmplName: EventSecurityAlert, raw: "no separator", wantOK: false},
		{name: "unknown variable for that event", tmplName: EventTest, raw: "Subject: s\n---\n{task_name}\n", wantOK: false},
		{name: "global variables are known everywhere", tmplName: EventTest, raw: "Subject: s\n---\n{app_name} {year}\n", wantOK: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateRaw(tc.tmplName, tc.raw)
			if v.OK() != tc.wantOK {
				t.Fatalf("OK() = %v (errors %v), want %v", v.OK(), v.Errors, tc.wantOK)
			}
		})
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"fqdn", "fqdnn", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
	}

	for _, tc := range tests {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			if got := editDistance(tc.a, tc.b); got != tc.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestValidVarName(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"fqdn", true},
		{"app_name", true},
		{"a1", true},
		{"", false},
		{"With Space", false},
		{"UPPER", false},
		{"../etc/passwd", false},
		{"dot.name", false},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := validVarName(tc.in); got != tc.want {
				t.Errorf("validVarName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
