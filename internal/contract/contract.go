// Package contract is the Go mirror of the TypeScript notation of record for the commands' inputs and outputs and the
// files' records. It imports nothing internal, and no other package declares its own copy of a union value, an exit
// code or a named constant.
//
// The mirror's rules:
//   - one named string type per union of string literals, whose constants carry the type's name as a prefix and keep
//     the notation's values unchanged (ErrorCode "failure" is ErrorCodeFailure; ReproTarget "wallet-backend-729" is
//     ReproTargetWalletBackend729; a "." in a value becomes "_", so SdkVersion "17.2.0" is SdkVersion17_2_0);
//   - one struct per interface, whose json tags are the notation's property names in the notation's order; only the
//     optional property (CatalogueEntry.networkRefusalEvidence) carries omitempty; a property typed `X | null` is a
//     pointer without omitempty, so nil is written as null;
//   - a property typed with a string literal (CatalogueSchemaResult.check) has the union type the literal belongs to;
//     a property typed `typeof C` has the constant's Go type;
//   - JsonValue and JsonObject are json.RawMessage: a decoded value keeps its bytes exactly, numbers and key order
//     included;
//   - the exit codes are values of the integer type ExitCode, gathered in EXIT_CODES;
//   - every named constant under the notation's own spelling.
//
// A slice field is written as [] only when it is non-nil: the code that builds a record sets every slice it writes,
// because no record ever holds null for an array.
//
// parity_test.go holds this file to the TypeScript notation of record, when present beside the module.
package contract

import "encoding/json"

// ─── Opaque JSON payloads ───────────────────────────────────────────────────────────────────────────────────────────

// JsonValue is any JSON value as it appears in a decoded entry's JSON or in the catalogue, kept byte for byte.
type JsonValue = json.RawMessage

// JsonObject is a JSON object as it appears in a decoded entry's JSON or in the catalogue (a wallet-backend stored
// row), kept byte for byte; that it is an object is the schema's to check.
type JsonObject = json.RawMessage

// ─── Exit codes and the standard-error line ─────────────────────────────────────────────────────────────────────────

// ExitCode is a process exit code of the exit-code map.
type ExitCode int

// EXIT_CODES is the exit-code map: 0 success, 1 a check or reproduction failed, 2 a usage error, 3 the environment is
// missing something the command needs. cmd/ltk/exit.go is the one place that maps an error to
// one of these.
var EXIT_CODES = struct {
	// Success: every check passed, the comparison found only ledger-dependent differences, or the reproduction
	// showed the expected before/after behaviour.
	Success ExitCode
	// Failure: a check or reproduction failed, a shape's submission or capture failed, or a comparison found a
	// difference outside the ledger-dependent list.
	Failure ExitCode
	// UsageError: an unknown command, flag, argument or `--shape <entry id>`.
	UsageError ExitCode
	// EnvironmentMissing: Docker with Linux containers, or a digest-pinned image that cannot be pulled, is missing.
	EnvironmentMissing ExitCode
}{
	Success:            0,
	Failure:            1,
	UsageError:         2,
	EnvironmentMissing: 3,
}

// ErrorCode is the code carried on the standard-error line; each one corresponds to one non-zero exit code.
type ErrorCode string

// The ErrorCode values.
const (
	ErrorCodeFailure            ErrorCode = "failure"
	ErrorCodeUsageError         ErrorCode = "usage_error"
	ErrorCodeEnvironmentMissing ErrorCode = "environment_missing"
)

// ErrorLine is the one record written to standard error on a non-zero exit: one line of JSON.
type ErrorLine struct {
	Code ErrorCode `json:"code"`
	// Message is human-readable; for environment_missing it states what is missing.
	Message string `json:"message"`
}

// ─── Protocol versions ──────────────────────────────────────────────────────────────────────────────────────────────

// FALLBACK_PROTOCOL_VERSION is the protocol of a shape whose recipe cannot be made to work at the current stable
// protocol.
const FALLBACK_PROTOCOL_VERSION = 28

// WALLET_BACKEND_729_PROTOCOL_VERSION is the protocol of the wallet-backend 729 reproduction's own network.
const WALLET_BACKEND_729_PROTOCOL_VERSION = 28

// ─── Catalogue ──────────────────────────────────────────────────────────────────────────────────────────────────────

// ExpectedValueGroup is one of the two expected-value groups: values every generation reproduces, and values that
// depend on the ledger the shape was captured in.
type ExpectedValueGroup string

// The ExpectedValueGroup values.
const (
	ExpectedValueGroupReproducedByEveryRun ExpectedValueGroup = "reproduced_by_every_run"
	ExpectedValueGroupLedgerDependent      ExpectedValueGroup = "ledger_dependent"
)

// LedgerDependentField is a field that depends on the ledger, and so may differ between two generations.
type LedgerDependentField string

// The LedgerDependentField values.
const (
	LedgerDependentFieldLedgerNumber         LedgerDependentField = "ledger_number"
	LedgerDependentFieldCloseTime            LedgerDependentField = "close_time"
	LedgerDependentFieldPreviousLedgerHash   LedgerDependentField = "previous_ledger_hash"
	LedgerDependentFieldBucketListHash       LedgerDependentField = "bucket_list_hash"
	LedgerDependentFieldTransactionSetHash   LedgerDependentField = "transaction_set_hash"
	LedgerDependentFieldConsensusSignature   LedgerDependentField = "consensus_signature"
	LedgerDependentFieldFeePool              LedgerDependentField = "fee_pool"
	LedgerDependentFieldLastModifiedLedger   LedgerDependentField = "last_modified_ledger"
	LedgerDependentFieldSourceSequenceNumber LedgerDependentField = "source_sequence_number"
	LedgerDependentFieldTransactionSignature LedgerDependentField = "transaction_signature"
	LedgerDependentFieldTransactionHash      LedgerDependentField = "transaction_hash"
	// LedgerDependentFieldLedgerHash is the ledger header's own hash, which covers the fields above.
	LedgerDependentFieldLedgerHash LedgerDependentField = "ledger_hash"
	// LedgerDependentFieldTransactionResultSetHash is the header's tx_set_result_hash, which covers the transaction's
	// result pairs and so its hash.
	LedgerDependentFieldTransactionResultSetHash LedgerDependentField = "transaction_result_set_hash"
)

// ReproducedValue is one expected value of the reproduced-by-every-run group.
type ReproducedValue struct {
	// Pointer is the JSON pointer into the entry's decoded JSON where the value is read back out.
	Pointer string    `json:"pointer"`
	Value   JsonValue `json:"value"`
}

// LedgerDependentValue is one expected value of the ledger-dependent group.
type LedgerDependentValue struct {
	Field LedgerDependentField `json:"field"`
	// Pointer is the JSON pointer into the entry's decoded JSON where the value is read back out.
	Pointer string    `json:"pointer"`
	Value   JsonValue `json:"value"`
}

// ExpectedDecodedValues is an entry's expected decoded values, split into their two groups.
type ExpectedDecodedValues struct {
	ReproducedByEveryRun []ReproducedValue      `json:"reproducedByEveryRun"`
	LedgerDependent      []LedgerDependentValue `json:"ledgerDependent"`
}

// NetworkRefusalResultCode is the network's result code for the text-order pool submission, verbatim.
type NetworkRefusalResultCode string

// The NetworkRefusalResultCode values.
const (
	NetworkRefusalResultCodeChangeTrustMalformed NetworkRefusalResultCode = "CHANGE_TRUST_MALFORMED"
)

// NetworkRefusalEvidence is entry 2's evidence that the network refused the same pool with its assets in text order.
type NetworkRefusalEvidence struct {
	// SubmittedEnvelopeXdr is the base64 XDR of the submitted envelope, its assets in text order.
	SubmittedEnvelopeXdr string                   `json:"submittedEnvelopeXdr"`
	ResultCode           NetworkRefusalResultCode `json:"resultCode"`
}

// CatalogueEntry is one entry's catalogue record.
type CatalogueEntry struct {
	ID                    string                `json:"id"`
	Cap                   string                `json:"cap"`
	ProtocolVersion       int                   `json:"protocolVersion"`
	ProtocolFallback      bool                  `json:"protocolFallback"`
	Source                string                `json:"source"`
	Synthetic             bool                  `json:"synthetic"`
	ExpectedDecodedValues ExpectedDecodedValues `json:"expectedDecodedValues"`
	FilePath              string                `json:"filePath"`
	XdrType               string                `json:"xdrType"`
	Sha256                string                `json:"sha256"`
	Length                int                   `json:"length"`
	// NetworkRefusalEvidence is present on entry 2 only.
	NetworkRefusalEvidence *NetworkRefusalEvidence `json:"networkRefusalEvidence,omitempty"`
}

// Catalogue is the catalogue file of a corpus folder.
type Catalogue struct {
	Entries []CatalogueEntry `json:"entries"`
}

// ─── ltk generate ───────────────────────────────────────────────────────────────────────────────────────────────────

// GenerateInput holds the flags of `ltk generate`; exactly one of All or Shape is given.
type GenerateInput struct {
	All   bool    `json:"all"`
	Shape *string `json:"shape"`
	Out   string  `json:"out"`
}

// ShapeGenerationResult is the outcome of generating one shape.
type ShapeGenerationResult struct {
	ID               string  `json:"id"`
	ProtocolVersion  int     `json:"protocolVersion"`
	ProtocolFallback bool    `json:"protocolFallback"`
	Generated        bool    `json:"generated"`
	Error            *string `json:"error"`
}

// GenerateReport is the output of `ltk generate`.
type GenerateReport struct {
	Out    string                  `json:"out"`
	Shapes []ShapeGenerationResult `json:"shapes"`
	// Check is nil when generation failed before the checks could run.
	Check *CheckReport `json:"check"`
}

// ─── ltk check ──────────────────────────────────────────────────────────────────────────────────────────────────────

// CheckInput holds the flags and arguments of `ltk check <folder> [--also-go]`.
type CheckInput struct {
	Folder string `json:"folder"`
	AlsoGo bool   `json:"alsoGo"`
}

// CheckName is one of the checks `ltk check` runs.
type CheckName string

// The CheckName values.
const (
	CheckNameCatalogueSchema CheckName = "catalogue_schema"
	CheckNameFileIntegrity   CheckName = "file_integrity"
	CheckNameRoundTrip       CheckName = "round_trip"
	CheckNameExpectedValues  CheckName = "expected_values"
	CheckNameGoSdkRoundTrip  CheckName = "go_sdk_round_trip"
)

// CHECK_ORDER is the order in which the checks run and are reported: the catalogue schema, file integrity, the round
// trip and the expected values, then the optional Go SDK cross-check. It is an array, so a caller holds a copy and
// cannot reorder it.
var CHECK_ORDER = [...]CheckName{
	CheckNameCatalogueSchema,
	CheckNameFileIntegrity,
	CheckNameRoundTrip,
	CheckNameExpectedValues,
	CheckNameGoSdkRoundTrip,
}

// SchemaFailure is one catalogue validation failure, named by its JSON pointer.
type SchemaFailure struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// CatalogueSchemaResult is the first check: the catalogue validated against the JSON Schema in schemas/.
type CatalogueSchemaResult struct {
	Check    CheckName       `json:"check"`
	Passed   bool            `json:"passed"`
	Failures []SchemaFailure `json:"failures"`
}

// FileIntegrityResult is the second check: the SHA-256 and length of the entry's XDR file against the catalogue.
type FileIntegrityResult struct {
	Check          CheckName `json:"check"`
	Passed         bool      `json:"passed"`
	Sha256         string    `json:"sha256"`
	Length         int       `json:"length"`
	RecordedSha256 string    `json:"recordedSha256"`
	RecordedLength int       `json:"recordedLength"`
}

// RoundTripResult is the third check: decoded and re-encoded with Stellar's Rust XDR tool, compared byte for byte.
type RoundTripResult struct {
	Check         CheckName `json:"check"`
	Passed        bool      `json:"passed"`
	BytesCompared int       `json:"bytesCompared"`
}

// GoSdkRoundTripResult is the `--also-go` cross-check with the Go SDK's XDR code.
type GoSdkRoundTripResult struct {
	Check         CheckName `json:"check"`
	Passed        bool      `json:"passed"`
	BytesCompared int       `json:"bytesCompared"`
}

// ValueMismatch is one expected value whose value read back from the decoded form differs from the catalogue.
type ValueMismatch struct {
	Pointer  string    `json:"pointer"`
	Recorded JsonValue `json:"recorded"`
	Decoded  JsonValue `json:"decoded"`
}

// ExpectedValuesResult is the fourth check: the expected values of both groups, read again from the decoded form.
type ExpectedValuesResult struct {
	Check      CheckName       `json:"check"`
	Passed     bool            `json:"passed"`
	Mismatches []ValueMismatch `json:"mismatches"`
}

// EntryCheckResult is one entry's ordered check results.
type EntryCheckResult struct {
	ID             string               `json:"id"`
	FileIntegrity  FileIntegrityResult  `json:"fileIntegrity"`
	RoundTrip      RoundTripResult      `json:"roundTrip"`
	ExpectedValues ExpectedValuesResult `json:"expectedValues"`
	// GoSdkRoundTrip is nil unless `--also-go` was given.
	GoSdkRoundTrip *GoSdkRoundTripResult `json:"goSdkRoundTrip"`
}

// CheckReport is the output of `ltk check`; the command exits with failure when Passed is false.
type CheckReport struct {
	Folder          string                `json:"folder"`
	CatalogueSchema CatalogueSchemaResult `json:"catalogueSchema"`
	Entries         []EntryCheckResult    `json:"entries"`
	Passed          bool                  `json:"passed"`
}

// ─── ltk list ───────────────────────────────────────────────────────────────────────────────────────────────────────

// ListInput holds the flags and arguments of `ltk list <folder> [--json]`.
type ListInput struct {
	Folder string `json:"folder"`
	JSON   bool   `json:"json"`
}

// ListEntry is one listed entry, its names those of the catalogue.
type ListEntry struct {
	ID              string `json:"id"`
	Cap             string `json:"cap"`
	ProtocolVersion int    `json:"protocolVersion"`
	XdrType         string `json:"xdrType"`
	Length          int    `json:"length"`
	Synthetic       bool   `json:"synthetic"`
}

// ListOutput is the output of `ltk list --json`.
type ListOutput = []ListEntry

// ─── ltk compare ────────────────────────────────────────────────────────────────────────────────────────────────────

// CompareInput holds the arguments of `ltk compare <folder A> <folder B>`.
type CompareInput struct {
	FolderA string `json:"folderA"`
	FolderB string `json:"folderB"`
}

// FieldDifference is one field whose value differs between the two generations.
type FieldDifference struct {
	Pointer string `json:"pointer"`
	// LedgerDependentField is nil when the field is not on the ledger-dependent list; a nil here fails the comparison.
	LedgerDependentField *LedgerDependentField `json:"ledgerDependentField"`
	ValueA               JsonValue             `json:"valueA"`
	ValueB               JsonValue             `json:"valueB"`
}

// EntryComparison is the per-entry comparison.
type EntryComparison struct {
	ID     string            `json:"id"`
	Equal  []string          `json:"equal"`
	Differ []FieldDifference `json:"differ"`
}

// CompareReport is the output of `ltk compare`; Passed is true only when every difference is ledger-dependent.
type CompareReport struct {
	FolderA string            `json:"folderA"`
	FolderB string            `json:"folderB"`
	Entries []EntryComparison `json:"entries"`
	Passed  bool              `json:"passed"`
}

// ─── ltk repro ──────────────────────────────────────────────────────────────────────────────────────────────────────

// ReproTarget is the `ltk repro <target>` argument, verbatim.
type ReproTarget string

// The ReproTarget values.
const (
	ReproTargetWalletBackend729 ReproTarget = "wallet-backend-729"
	ReproTargetJsSdk1766        ReproTarget = "js-sdk-1766"
)

// ReproInput holds the argument of `ltk repro <target>`.
type ReproInput struct {
	Target ReproTarget `json:"target"`
}

// FixRole is which side of the fix a commit or release is on.
type FixRole string

// The FixRole values.
const (
	FixRoleBeforeFix FixRole = "before_fix"
	FixRoleFix       FixRole = "fix"
)

// WalletBackendCommit is a wallet-backend commit under test.
type WalletBackendCommit string

// The WalletBackendCommit values.
const (
	WalletBackendCommit2a0a84ea WalletBackendCommit = "2a0a84ea"
	WalletBackendCommit2c873353 WalletBackendCommit = "2c873353"
)

// PRE_FIX_RETRY_LIMIT is the number of retries of the same ledger after which the pre-fix commit's failure is
// captured.
const PRE_FIX_RETRY_LIMIT = 5

// PRE_FIX_RESTART_COUNT is how many times the pre-fix commit is restarted to show the same failure again.
const PRE_FIX_RESTART_COUNT = 1

// COMMIT_RUN_DEADLINE_MINUTES is the safety deadline, in minutes, of each run of a wallet-backend commit. A pre-fix
// run that reaches it without showing the failure is reported as not reproduced.
const COMMIT_RUN_DEADLINE_MINUTES = 15

// WalletBackendCommitResult is the per-commit ingestion report for wallet-backend 729.
type WalletBackendCommitResult struct {
	Commit   WalletBackendCommit `json:"commit"`
	Role     FixRole             `json:"role"`
	Ledger   int                 `json:"ledger"`
	Ingested bool                `json:"ingested"`
	// Error is nil when Ingested is true.
	Error *string `json:"error"`
	// StoredRow is nil when Ingested is false.
	StoredRow      *JsonObject `json:"storedRow"`
	Retries        int         `json:"retries"`
	Restarts       int         `json:"restarts"`
	LogPath        string      `json:"logPath"`
	StoreStatePath string      `json:"storeStatePath"`
}

// WalletBackend729Report is the output of `ltk repro wallet-backend-729`.
type WalletBackend729Report struct {
	Target ReproTarget `json:"target"`
	// ProtocolVersion is WALLET_BACKEND_729_PROTOCOL_VERSION.
	ProtocolVersion int                         `json:"protocolVersion"`
	Commits         []WalletBackendCommitResult `json:"commits"`
	Passed          bool                        `json:"passed"`
}

// SdkVersion is an @stellar/stellar-sdk release under test.
type SdkVersion string

// The SdkVersion values.
const (
	SdkVersion17_2_0 SdkVersion = "17.2.0"
	SdkVersion17_2_1 SdkVersion = "17.2.1"
)

// SdkReproStep is a step of the SDK reproduction script.
type SdkReproStep string

// The SdkReproStep values.
const (
	SdkReproStepBuild    SdkReproStep = "build"
	SdkReproStepDecode   SdkReproStep = "decode"
	SdkReproStepReEncode SdkReproStep = "re_encode"
)

// SdkStepResult is the outcome of one script step.
type SdkStepResult struct {
	Step   SdkReproStep `json:"step"`
	Passed bool         `json:"passed"`
	// Error is the thrown message when the step failed; otherwise nil.
	Error *string `json:"error"`
}

// SdkVersionResult is the per-release verdict of the SDK reproduction script.
type SdkVersionResult struct {
	Version            SdkVersion      `json:"version"`
	Role               FixRole         `json:"role"`
	Steps              []SdkStepResult `json:"steps"`
	RoundTripIdentical bool            `json:"roundTripIdentical"`
}

// JsSdk1766Report is the output of `ltk repro js-sdk-1766`.
type JsSdk1766Report struct {
	Target   ReproTarget        `json:"target"`
	Versions []SdkVersionResult `json:"versions"`
	Passed   bool               `json:"passed"`
}

// ReproReport (WalletBackend729Report | JsSdk1766Report) has no Go type: each reproduction writes its own report
// type, selected by its target.
