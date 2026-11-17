package contract

import "reflect"

// The registry of the mirror: what the Contract parity test compares with the TypeScript notation of record at run
// time, beside its reading of this package's source.

// Unions maps each union of string literals in the notation to its Go constants' values, in the notation's
// order.
var Unions = map[string][]string{
	"ErrorCode":          {string(ErrorCodeFailure), string(ErrorCodeUsageError), string(ErrorCodeEnvironmentMissing)},
	"ExpectedValueGroup": {string(ExpectedValueGroupReproducedByEveryRun), string(ExpectedValueGroupLedgerDependent)},
	"LedgerDependentField": {
		string(LedgerDependentFieldLedgerNumber), string(LedgerDependentFieldCloseTime),
		string(LedgerDependentFieldPreviousLedgerHash), string(LedgerDependentFieldBucketListHash),
		string(LedgerDependentFieldTransactionSetHash), string(LedgerDependentFieldConsensusSignature),
		string(LedgerDependentFieldFeePool), string(LedgerDependentFieldLastModifiedLedger),
		string(LedgerDependentFieldSourceSequenceNumber), string(LedgerDependentFieldTransactionSignature),
		string(LedgerDependentFieldTransactionHash), string(LedgerDependentFieldLedgerHash),
		string(LedgerDependentFieldTransactionResultSetHash),
	},
	"NetworkRefusalResultCode": {string(NetworkRefusalResultCodeChangeTrustMalformed)},
	"CheckName": {
		string(CheckNameCatalogueSchema), string(CheckNameFileIntegrity), string(CheckNameRoundTrip),
		string(CheckNameExpectedValues), string(CheckNameGoSdkRoundTrip),
	},
	"ReproTarget":         {string(ReproTargetWalletBackend729), string(ReproTargetJsSdk1766)},
	"FixRole":             {string(FixRoleBeforeFix), string(FixRoleFix)},
	"WalletBackendCommit": {string(WalletBackendCommit2a0a84ea), string(WalletBackendCommit2c873353)},
	"SdkVersion":          {string(SdkVersion17_2_0), string(SdkVersion17_2_1)},
	"SdkReproStep":        {string(SdkReproStepBuild), string(SdkReproStepDecode), string(SdkReproStepReEncode)},
}

// Interfaces maps each interface of the notation to its Go struct.
var Interfaces = map[string]reflect.Type{
	"ErrorLine":                 reflect.TypeFor[ErrorLine](),
	"ReproducedValue":           reflect.TypeFor[ReproducedValue](),
	"LedgerDependentValue":      reflect.TypeFor[LedgerDependentValue](),
	"ExpectedDecodedValues":     reflect.TypeFor[ExpectedDecodedValues](),
	"NetworkRefusalEvidence":    reflect.TypeFor[NetworkRefusalEvidence](),
	"CatalogueEntry":            reflect.TypeFor[CatalogueEntry](),
	"Catalogue":                 reflect.TypeFor[Catalogue](),
	"GenerateInput":             reflect.TypeFor[GenerateInput](),
	"ShapeGenerationResult":     reflect.TypeFor[ShapeGenerationResult](),
	"GenerateReport":            reflect.TypeFor[GenerateReport](),
	"CheckInput":                reflect.TypeFor[CheckInput](),
	"SchemaFailure":             reflect.TypeFor[SchemaFailure](),
	"CatalogueSchemaResult":     reflect.TypeFor[CatalogueSchemaResult](),
	"FileIntegrityResult":       reflect.TypeFor[FileIntegrityResult](),
	"RoundTripResult":           reflect.TypeFor[RoundTripResult](),
	"GoSdkRoundTripResult":      reflect.TypeFor[GoSdkRoundTripResult](),
	"ValueMismatch":             reflect.TypeFor[ValueMismatch](),
	"ExpectedValuesResult":      reflect.TypeFor[ExpectedValuesResult](),
	"EntryCheckResult":          reflect.TypeFor[EntryCheckResult](),
	"CheckReport":               reflect.TypeFor[CheckReport](),
	"ListInput":                 reflect.TypeFor[ListInput](),
	"ListEntry":                 reflect.TypeFor[ListEntry](),
	"CompareInput":              reflect.TypeFor[CompareInput](),
	"FieldDifference":           reflect.TypeFor[FieldDifference](),
	"EntryComparison":           reflect.TypeFor[EntryComparison](),
	"CompareReport":             reflect.TypeFor[CompareReport](),
	"ReproInput":                reflect.TypeFor[ReproInput](),
	"WalletBackendCommitResult": reflect.TypeFor[WalletBackendCommitResult](),
	"WalletBackend729Report":    reflect.TypeFor[WalletBackend729Report](),
	"SdkStepResult":             reflect.TypeFor[SdkStepResult](),
	"SdkVersionResult":          reflect.TypeFor[SdkVersionResult](),
	"JsSdk1766Report":           reflect.TypeFor[JsSdk1766Report](),
}

// ExitCodes maps each key of the notation's EXIT_CODES to its code.
var ExitCodes = map[string]int{
	"success":            int(EXIT_CODES.Success),
	"failure":            int(EXIT_CODES.Failure),
	"usageError":         int(EXIT_CODES.UsageError),
	"environmentMissing": int(EXIT_CODES.EnvironmentMissing),
}

// Constants maps each numeric named constant of the notation to its value.
var Constants = map[string]int{
	"FALLBACK_PROTOCOL_VERSION":           FALLBACK_PROTOCOL_VERSION,
	"WALLET_BACKEND_729_PROTOCOL_VERSION": WALLET_BACKEND_729_PROTOCOL_VERSION,
	"PRE_FIX_RETRY_LIMIT":                 PRE_FIX_RETRY_LIMIT,
	"PRE_FIX_RESTART_COUNT":               PRE_FIX_RESTART_COUNT,
	"COMMIT_RUN_DEADLINE_MINUTES":         COMMIT_RUN_DEADLINE_MINUTES,
}
