package autostart

import (
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf16"
)

// This file renders the three unit formats. It is compiled on every platform —
// no build tags — so the templates can be unit-tested anywhere, not only on the
// OS that consumes them. Only the code that *installs* them is per-platform.

// restartDelaySeconds is how long the supervisor waits before restarting a
// crashed watcher. Long enough that a persistent failure does not spin the CPU,
// short enough that a transient one heals before the user notices.
const restartDelaySeconds = 5

// RenderSystemdUnit builds the systemd *user* unit.
//
// Restart=on-failure (not =always) is deliberate: `weft autostart run` exits 0
// when there is no active profile, and =always would turn that clean exit into
// a crash-loop.
func RenderSystemdUnit(o Options) string {
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=weft — AI rules watcher\n")
	b.WriteString("Documentation=https://github.com/jophira/weft\n")
	b.WriteString("After=default.target\n\n")

	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", systemdExec(o))
	b.WriteString("Restart=on-failure\n")
	fmt.Fprintf(&b, "RestartSec=%d\n", restartDelaySeconds)
	b.WriteString("Environment=WEFT_AUTOSTART=1\n\n")

	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.String()
}

// systemdExec renders an ExecStart value. systemd splits the line on
// whitespace unless words are double-quoted, so every word is quoted — paths
// with spaces are common enough on macOS-style home layouts to be worth it.
func systemdExec(o Options) string {
	words := append([]string{o.BinPath}, RunArgs(o)...)
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, `"`+systemdEscape(w)+`"`)
	}
	return strings.Join(quoted, " ")
}

// systemdEscape escapes the two characters that terminate or continue a
// double-quoted systemd word.
func systemdEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// RenderLaunchdPlist builds the LaunchAgent property list.
//
// KeepAlive uses SuccessfulExit=false rather than a bare <true/> for the same
// reason systemd uses on-failure: a clean exit 0 (no active profile) must not
// be restarted.
func RenderLaunchdPlist(o Options) (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	b.WriteString("  <key>Label</key>\n")
	if err := writeXMLString(&b, "  ", Label); err != nil {
		return "", err
	}
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, w := range append([]string{o.BinPath}, RunArgs(o)...) {
		if err := writeXMLString(&b, "    ", w); err != nil {
			return "", err
		}
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("  <key>KeepAlive</key>\n  <dict>\n    <key>SuccessfulExit</key>\n    <false/>\n  </dict>\n")
	b.WriteString("  <key>ProcessType</key>\n")
	if err := writeXMLString(&b, "  ", "Background"); err != nil {
		return "", err
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String(), nil
}

// RenderTaskXML builds the Windows Task Scheduler definition for a logon task.
//
// A scheduled task is used rather than the Startup folder or HKCU\...\Run:
// both of those launch weft as a visible console process, flashing a window on
// every boot. <Hidden>true</Hidden> plus a task keeps it silent.
//
// Task Scheduler requires UTF-16 XML on disk; the caller encodes it.
func RenderTaskXML(o Options) (string, error) {
	args, err := xmlEscape(windowsArgs(RunArgs(o)))
	if err != nil {
		return "", err
	}
	cmd, err := xmlEscape(o.BinPath)
	if err != nil {
		return "", err
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>weft — AI rules watcher</Description>
    <URI>\` + TaskName + `</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + cmd + `</Command>
      <Arguments>` + args + `</Arguments>
    </Exec>
  </Actions>
</Task>
`, nil
}

// utf16BOM is the byte-order mark schtasks /XML requires at the head of the
// task definition; it also tells DecodeUTF16LE where the text really starts.
const utf16BOM = 0xFEFF

// EncodeUTF16LE renders s as UTF-16LE with a byte-order mark. Task Scheduler's
// /XML switch rejects UTF-8 outright, so the encoding is part of rendering the
// unit rather than an implementation detail of writing it.
func EncodeUTF16LE(s string) []byte {
	units := append([]uint16{utf16BOM}, utf16.Encode([]rune(s))...)
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

// DecodeUTF16LE reverses EncodeUTF16LE, tolerating a missing BOM and an odd
// trailing byte: a truncated definition is read as far as it goes rather than
// erroring, because the result only feeds a substring check in Status.
func DecodeUTF16LE(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(b[i:]))
	}
	if len(units) > 0 && units[0] == utf16BOM {
		units = units[1:]
	}
	return string(utf16.Decode(units))
}

// windowsArgs joins arguments using the quoting rules CommandLineToArgvW
// applies — the inverse of how the task host will split the string again.
func windowsArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if a != "" && !strings.ContainsAny(a, ` "`) {
			quoted = append(quoted, a)
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
	}
	return strings.Join(quoted, " ")
}

// writeXMLString appends an indented plist <string> element.
func writeXMLString(b *strings.Builder, indent, value string) error {
	escaped, err := xmlEscape(value)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "%s<string>%s</string>\n", indent, escaped)
	return nil
}

// xmlEscape escapes text for inclusion in an XML element body.
func xmlEscape(s string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", fmt.Errorf("autostart: escaping %q: %w", s, err)
	}
	return b.String(), nil
}
