package autostart

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func TestRunArgs_should_be_bare_when_no_config_or_profile(t *testing.T) {
	got := RunArgs(Options{BinPath: "/usr/local/bin/weft"})
	want := []string{"autostart", "run"}
	if len(got) != len(want) {
		t.Fatalf("RunArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RunArgs = %v, want %v", got, want)
		}
	}
}

func TestRunArgs_should_carry_config_and_pinned_profile(t *testing.T) {
	got := strings.Join(RunArgs(Options{
		BinPath:    "/usr/local/bin/weft",
		ConfigFile: "/tmp/iso/config.yaml",
		Profile:    "hybrid",
	}), " ")
	want := "autostart run --config /tmp/iso/config.yaml --profile hybrid"
	if got != want {
		t.Fatalf("RunArgs = %q, want %q", got, want)
	}
}

func TestRenderSystemdUnit_should_restart_on_failure_only(t *testing.T) {
	unit := RenderSystemdUnit(Options{BinPath: "/usr/local/bin/weft"})

	// Restart=always would turn the deliberate exit-0 "no active profile" path
	// into a crash-loop — the exact failure mode #212 calls out.
	if !strings.Contains(unit, "Restart=on-failure") {
		t.Errorf("unit missing Restart=on-failure:\n%s", unit)
	}
	if strings.Contains(unit, "Restart=always") {
		t.Errorf("unit must not use Restart=always:\n%s", unit)
	}
	for _, want := range []string{"[Unit]", "[Service]", "[Install]", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestRenderSystemdUnit_should_quote_exec_words_for_paths_with_spaces(t *testing.T) {
	unit := RenderSystemdUnit(Options{
		BinPath:    "/home/a b/bin/weft",
		ConfigFile: "/home/a b/cfg.yaml",
	})
	want := `ExecStart="/home/a b/bin/weft" "autostart" "run" "--config" "/home/a b/cfg.yaml"`
	if !strings.Contains(unit, want) {
		t.Fatalf("unit ExecStart not quoted as expected:\n%s", unit)
	}
}

func TestSystemdEscape_should_escape_quotes_and_backslashes(t *testing.T) {
	if got, want := systemdEscape(`a"b\c`), `a\"b\\c`; got != want {
		t.Fatalf("systemdEscape = %q, want %q", got, want)
	}
}

func TestRenderLaunchdPlist_should_be_valid_xml_with_program_arguments(t *testing.T) {
	plist, err := RenderLaunchdPlist(Options{BinPath: "/usr/local/bin/weft", Profile: "work"})
	if err != nil {
		t.Fatalf("RenderLaunchdPlist: %v", err)
	}
	if err := xml.Unmarshal([]byte(plist), new(struct {
		XMLName xml.Name `xml:"plist"`
	})); err != nil {
		t.Fatalf("plist is not well-formed XML: %v\n%s", err, plist)
	}
	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>/usr/local/bin/weft</string>",
		"<string>--profile</string>",
		"<string>work</string>",
		"<key>RunAtLoad</key>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestRenderLaunchdPlist_should_not_keepalive_on_clean_exit(t *testing.T) {
	plist, err := RenderLaunchdPlist(Options{BinPath: "/usr/local/bin/weft"})
	if err != nil {
		t.Fatalf("RenderLaunchdPlist: %v", err)
	}
	// KeepAlive as a bare <true/> would restart the agent after the clean
	// exit-0 "no active profile" path; SuccessfulExit=false does not.
	if !strings.Contains(plist, "<key>SuccessfulExit</key>\n    <false/>") {
		t.Fatalf("plist KeepAlive must be conditional on SuccessfulExit:\n%s", plist)
	}
}

func TestRenderLaunchdPlist_should_escape_xml_metacharacters_in_paths(t *testing.T) {
	plist, err := RenderLaunchdPlist(Options{BinPath: `/home/a&b/weft`})
	if err != nil {
		t.Fatalf("RenderLaunchdPlist: %v", err)
	}
	if !strings.Contains(plist, "/home/a&amp;b/weft") {
		t.Fatalf("plist did not escape '&':\n%s", plist)
	}
}

func TestRenderTaskXML_should_be_valid_hidden_logon_task(t *testing.T) {
	taskXML, err := RenderTaskXML(Options{BinPath: `C:\Users\p\weft.exe`})
	if err != nil {
		t.Fatalf("RenderTaskXML: %v", err)
	}
	// The document declares UTF-16 because schtasks requires it on disk; the
	// string itself is Go's UTF-8, so the decoder needs the charset passthrough.
	dec := xml.NewDecoder(strings.NewReader(taskXML))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	if err := dec.Decode(new(struct {
		XMLName xml.Name `xml:"Task"`
	})); err != nil {
		t.Fatalf("task XML is not well-formed: %v\n%s", err, taskXML)
	}
	// Hidden is what keeps a console window from flashing on every boot —
	// the reason a task is used instead of the Startup folder.
	for _, want := range []string{"<Hidden>true</Hidden>", "<LogonTrigger>", `<Command>C:\Users\p\weft.exe</Command>`} {
		if !strings.Contains(taskXML, want) {
			t.Errorf("task XML missing %q:\n%s", want, taskXML)
		}
	}
}

func TestRenderTaskXML_should_quote_arguments_containing_spaces(t *testing.T) {
	taskXML, err := RenderTaskXML(Options{
		BinPath:    `C:\weft.exe`,
		ConfigFile: `C:\Users\a b\config.yaml`,
	})
	if err != nil {
		t.Fatalf("RenderTaskXML: %v", err)
	}
	if !strings.Contains(taskXML, `&#34;C:\Users\a b\config.yaml&#34;`) {
		t.Fatalf("task XML did not quote the spaced path:\n%s", taskXML)
	}
}

func TestEncodeUTF16LE_should_lead_with_a_bom_and_round_trip(t *testing.T) {
	// schtasks /XML rejects UTF-8, so the BOM is not cosmetic — without it the
	// task import fails with an unhelpful parse error.
	const s = "<Task>weft — ünïcode</Task>"
	encoded := EncodeUTF16LE(s)

	if len(encoded) < 2 || encoded[0] != 0xFF || encoded[1] != 0xFE {
		t.Fatalf("encoded output does not start with a UTF-16LE BOM: % x", encoded[:min(4, len(encoded))])
	}
	if got := DecodeUTF16LE(encoded); got != s {
		t.Fatalf("round trip = %q, want %q", got, s)
	}
}

func TestDecodeUTF16LE_should_tolerate_a_truncated_definition(t *testing.T) {
	encoded := EncodeUTF16LE("weft")
	// Lop off a trailing half-unit — Status only substring-matches the result,
	// so reading as far as possible beats failing the whole status call.
	if got := DecodeUTF16LE(encoded[:len(encoded)-1]); got != "wef" {
		t.Fatalf("truncated decode = %q, want %q", got, "wef")
	}
}

func TestWindowsArgs_should_only_quote_when_needed(t *testing.T) {
	got := windowsArgs([]string{"autostart", "run", "--config", `C:\a b\c.yaml`})
	want := `autostart run --config "C:\a b\c.yaml"`
	if got != want {
		t.Fatalf("windowsArgs = %q, want %q", got, want)
	}
}
