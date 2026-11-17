package catalogue

// The Repro report writer: each reproduction's last report is written to repro/<target>.json under the working
// directory, whole, and the command prints the same bytes on standard output. repro/ holds those two reports and
// nothing else: the wallet-backend reproduction's logs and store dumps are written by its runner to a working folder
// outside the repository's tree.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/verticalhorizonai/ltk/internal/contract"
)

// ReproFolder is the folder of the reproductions' reports, relative to the working directory.
const ReproFolder = "repro"

// ReproReportPath is the path of target's report under dir: <dir>/repro/<target>.json, named by the target.
func ReproReportPath(dir string, target contract.ReproTarget) string {
	return filepath.Join(dir, ReproFolder, string(target)+".json")
}

// WriteWalletBackendReport writes rep to <dir>/repro/wallet-backend-729.json and returns the bytes written, which the
// command prints on standard output byte for byte. A report that is not in the wallet-backend report's shape is not
// written.
func WriteWalletBackendReport(dir string, rep contract.WalletBackend729Report) ([]byte, error) {
	if err := walletBackendShape(rep); err != nil {
		return nil, err
	}
	return writeReproReport(dir, contract.ReproTargetWalletBackend729, rep)
}

// WriteJsSdkReport writes rep to <dir>/repro/js-sdk-1766.json and returns the bytes written, which the command prints
// on standard output byte for byte. A report that is not in the js-sdk report's shape is not written.
func WriteJsSdkReport(dir string, rep contract.JsSdk1766Report) ([]byte, error) {
	if err := jsSdkShape(rep); err != nil {
		return nil, err
	}
	return writeReproReport(dir, contract.ReproTargetJsSdk1766, rep)
}

// EncodeReproReport renders a reproduction report as it is written and printed: one JSON document, two-space
// indentation, fields in the order of internal/contract, no HTML escaping, LF line endings and one trailing newline.
func EncodeReproReport(rep any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeReproReport creates <dir>/repro when it is absent and replaces the report file whole (a temporary file beside
// it, renamed into place: a reader never sees half a report, and a failed write leaves the previous report, if any,
// and no temporary file). Something that is not a regular file in the report's place fails the write, naming it.
func writeReproReport(dir string, target contract.ReproTarget, rep any) ([]byte, error) {
	b, err := EncodeReproReport(rep)
	if err != nil {
		return nil, err
	}
	folder := filepath.Join(dir, ReproFolder)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, fmt.Errorf("create the folder %s: %w", filepath.ToSlash(folder), err)
	}
	if err := writeWhole(ReproReportPath(dir, target), b); err != nil {
		return nil, err
	}
	return b, nil
}

// walletBackendShape refuses a report whose shape the wallet-backend report does not allow: another target or
// protocol, or commits that are not 2a0a84ea (before_fix) then 2c873353 (fix). It is a defect of the runner that made
// it.
func walletBackendShape(rep contract.WalletBackend729Report) error {
	want := []struct {
		commit contract.WalletBackendCommit
		role   contract.FixRole
	}{
		{contract.WalletBackendCommit2a0a84ea, contract.FixRoleBeforeFix},
		{contract.WalletBackendCommit2c873353, contract.FixRoleFix},
	}
	switch {
	case rep.Target != contract.ReproTargetWalletBackend729:
		return fmt.Errorf("the wallet-backend report's target is %q, not %q", rep.Target, contract.ReproTargetWalletBackend729)
	case rep.ProtocolVersion != contract.WALLET_BACKEND_729_PROTOCOL_VERSION:
		return fmt.Errorf("the wallet-backend report's protocolVersion is %d, not %d", rep.ProtocolVersion, contract.WALLET_BACKEND_729_PROTOCOL_VERSION)
	case len(rep.Commits) != len(want):
		return fmt.Errorf("the wallet-backend report holds %d commits, not %d", len(rep.Commits), len(want))
	}
	for i, w := range want {
		if c := rep.Commits[i]; c.Commit != w.commit || c.Role != w.role {
			return fmt.Errorf("the wallet-backend report's commits[%d] is %s (%s), not %s (%s)", i, c.Commit, c.Role, w.commit, w.role)
		}
	}
	return nil
}

// jsSdkShape refuses a report whose shape the js-sdk report does not allow: another target, versions that are not
// 17.2.0 (before_fix) then 17.2.1 (fix), or a version with no step or more than three. It is a defect of the runner
// that made it.
func jsSdkShape(rep contract.JsSdk1766Report) error {
	want := []struct {
		version contract.SdkVersion
		role    contract.FixRole
	}{
		{contract.SdkVersion17_2_0, contract.FixRoleBeforeFix},
		{contract.SdkVersion17_2_1, contract.FixRoleFix},
	}
	switch {
	case rep.Target != contract.ReproTargetJsSdk1766:
		return fmt.Errorf("the js-sdk report's target is %q, not %q", rep.Target, contract.ReproTargetJsSdk1766)
	case len(rep.Versions) != len(want):
		return fmt.Errorf("the js-sdk report holds %d versions, not %d", len(rep.Versions), len(want))
	}
	for i, w := range want {
		v := rep.Versions[i]
		if v.Version != w.version || v.Role != w.role {
			return fmt.Errorf("the js-sdk report's versions[%d] is %s (%s), not %s (%s)", i, v.Version, v.Role, w.version, w.role)
		}
		if len(v.Steps) < 1 || len(v.Steps) > 3 {
			return fmt.Errorf("the js-sdk report's version %s holds %d steps, not 1 to 3", v.Version, len(v.Steps))
		}
	}
	return nil
}
