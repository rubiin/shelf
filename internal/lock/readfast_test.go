package lock

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// decodeSlow is the general decoder the fast reader must agree with.
func decodeSlow(t *testing.T, contents []byte) LockedConfig {
	t.Helper()
	var locked LockedConfig
	if err := toml.Unmarshal(contents, &locked); err != nil {
		t.Fatalf("toml.Unmarshal: %v", err)
	}
	return locked
}

func TestFastReadMatchesTomlDecode(t *testing.T) {
	locked := LockedConfig{
		ConfigFingerprint: "c0b375d8b66872df321a3d6f450f1631c5e2bb687c0eac25bf581d23066b54a6",
		Profile:           "work",
		Shell:             "bash",
		Templates: map[string]string{
			"source": "{{ hooks?.pre | nl }}{% for file in files %}source \"{{ file }}\"\n{% endfor %}",
			"PATH":   `export PATH="{{ dir }}:$PATH"`,
			"quoted": "tab\there \"quoted\" and \\backslash\\",
			"emoji":  "🔥 unicode",
		},
		Plugins: []LockedPlugin{
			{
				Name:   "local",
				Source: "local:./src",
				Files:  []string{"/src/a.plugin.zsh", "/src/b.zsh"},
				Apply:  []string{"source", "PATH"},
			},
			{
				Name:   "hooked",
				Inline: "echo {{ name }}\nwith \"quotes\" and \\slashes\\",
				Hooks: map[string]string{
					"pre":       "echo pre",
					"post":      "echo post\nsecond line",
					"dot.ted":   "echo dotted",
					"with sp":   "echo spaced",
					"with\"q":   "echo quoted",
					"control\a": "echo bell",
				},
			},
			{
				Name:      "git",
				Source:    "github:owner/repo",
				URL:       "https://github.com/owner/repo",
				Rev:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Directory: "/data/repos/github.com/owner/repo",
				Files:     []string{"/data/repos/github.com/owner/repo/repo.plugin.zsh"},
				Apply:     []string{"source"},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "plugins.lock")
	if err := Write(path, locked); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	fast, ok := parseLockFast(contents)
	if !ok {
		t.Fatal("parseLockFast gave up on a lock this package wrote")
	}
	if slow := decodeSlow(t, contents); !reflect.DeepEqual(fast, slow) {
		t.Fatalf("fast reader diverged from TOML decode:\nfast: %#v\nslow: %#v", fast, slow)
	}

	decoded, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, fast) {
		t.Fatalf("Read diverged from the fast reader:\nread: %#v\nfast: %#v", decoded, fast)
	}
}

// Every lock this package writes must be readable by the fast reader, for any plugin shape.
func TestFastReadHandlesEveryWrittenLock(t *testing.T) {
	cases := map[string]LockedConfig{
		"empty": {},
		"fingerprint only": {
			ConfigFingerprint: "abc",
		},
		"inline with newlines": {
			Shell: "zsh",
			Plugins: []LockedPlugin{{
				Name:   "inline",
				Inline: "line one\nline two\n",
			}},
		},
		"hooks only": {
			Shell: "zsh",
			Plugins: []LockedPlugin{{
				Name:  "hooked",
				Hooks: map[string]string{"pre": "echo pre"},
			}},
		},
		"templates only": {
			Shell:     "bash",
			Templates: map[string]string{"source": "source {{ file }}\n"},
		},
		"empty file list": {
			Shell:   "zsh",
			Plugins: []LockedPlugin{{Name: "empty", Files: []string{}, Apply: []string{}}},
		},
		"windows style paths and spaces": {
			Shell: "zsh",
			Plugins: []LockedPlugin{{
				Name:      "spaced",
				Directory: `C:\Users\me\plugins\my plugin`,
				Files:     []string{`C:\Users\me\plugins\my plugin\init.zsh`},
			}},
		},
	}
	for name, locked := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plugins.lock")
			if err := Write(path, locked); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fast, ok := parseLockFast(contents)
			if !ok {
				t.Fatalf("parseLockFast gave up on:\n%s", contents)
			}
			if slow := decodeSlow(t, contents); !reflect.DeepEqual(fast, slow) {
				t.Fatalf("fast reader diverged:\nfast: %#v\nslow: %#v", fast, slow)
			}
		})
	}
}

func TestFastReadFallsBackToToml(t *testing.T) {
	base := LockedConfig{
		ConfigFingerprint: "abc",
		Shell:             "zsh",
		Templates:         map[string]string{"source": "source {{ file }}\n"},
		Plugins:           []LockedPlugin{{Name: "one", Files: []string{"/one.zsh"}}},
	}
	path := filepath.Join(t.TempDir(), "plugins.lock")
	if err := Write(path, base); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	unknown := map[string][]byte{
		"comment":            append([]byte("# a comment\n"), contents...),
		"comment at the end": append(append([]byte{}, contents...), []byte("# trailing\n")...),
		"unknown root key":   []byte("future_field = \"value\"\n" + string(contents)),
		"unknown table":      []byte(string(contents) + "\n[future]\nkey = \"value\"\n"),
		"unknown plugin key": []byte(string(contents) + "\n[[plugins]]\nname = \"x\"\nfuture = \"value\"\n"),
		"integer value":      []byte("future = 3\n" + string(contents)),
		"multiline string":   []byte("shell = \"\"\"zsh\"\"\"\n"),
		"inline table":       []byte(string(contents) + "\n[[plugins]]\nname = \"x\"\nhooks = { pre = \"echo pre\" }\n"),
		"array of integers":  []byte("shell = \"zsh\"\nfuture = [1, 2]\n"),
		"invalid toml":       []byte("shell = \"zsh\n"),
	}
	for name, modified := range unknown {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseLockFast(modified); ok {
				t.Fatal("fast reader accepted contents it does not understand")
			}
			// The fallback decodes the file exactly as the general decoder does, error included.
			var slow LockedConfig
			slowErr := toml.Unmarshal(modified, &slow)
			decoded, err := readLock(modified)
			if slowErr != nil {
				if err == nil {
					t.Fatalf("readLock accepted contents TOML rejects: %v", slowErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readLock: %v", err)
			}
			if !reflect.DeepEqual(decoded, slow) {
				t.Fatalf("readLock diverged from TOML decode:\nread: %#v\nslow: %#v", decoded, slow)
			}
		})
	}
}

// Nothing the fast reader accepts may decode differently (or not at all) as TOML.
func FuzzReadLockMatchesTomlDecoder(f *testing.F) {
	seed := LockedConfig{
		ConfigFingerprint: "abc",
		Profile:           "work",
		Shell:             "zsh",
		Templates:         map[string]string{"source": "source {{ file }}\n", "PATH": `export PATH="{{ dir }}:$PATH"`},
		Plugins: []LockedPlugin{
			{Name: "inline", Inline: "echo hi\n"},
			{Name: "hooked", Hooks: map[string]string{"pre": "echo pre"}},
			{Name: "local", Directory: "/src", Files: []string{"/src/a.zsh"}, Apply: []string{"source"}},
		},
	}
	path := filepath.Join(f.TempDir(), "plugins.lock")
	if err := Write(path, seed); err != nil {
		f.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(contents)
	f.Add([]byte("shell = \"zsh\"\n"))
	f.Add([]byte("[[plugins]]\nname = \"a\"\n"))
	f.Add([]byte("[[plugins]]\nhooks = { pre = \"echo\" }\n"))
	f.Add([]byte("templates = { source = \"x\" }\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fast, ok := parseLockFast(data)
		if !ok {
			return
		}
		var slow LockedConfig
		if err := toml.Unmarshal(data, &slow); err != nil {
			t.Fatalf("fast reader accepted contents TOML rejects: %v\ncontents:\n%s", err, data)
		}
		if !reflect.DeepEqual(fast, slow) {
			t.Fatalf("fast reader diverged from TOML decode:\nfast: %#v\nslow: %#v\ncontents:\n%s", fast, slow, data)
		}
	})
}
