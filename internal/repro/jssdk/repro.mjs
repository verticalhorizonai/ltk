// The js-stellar-sdk 1766 reproduction script. Its first form is feasibility/repro.cjs of the feasibility run of
// Nov 12, made before this tool. `ltk repro js-sdk-1766` copies it, with one release's package.json and
// package-lock.json, into a node:22-slim container, runs `npm ci --ignore-scripts`, then
// `node repro.mjs <asset code> <issuer A> <issuer B>`.
//
// The arguments are entry 2's asset code and its two issuers in key-byte order, from the tool's own seed definitions
// (internal/model/seeds.go): the pool the protocol accepts, whose issuers sort the other way as strkey text. No corpus
// file is read, no network is reached, nothing is signed.
//
// Steps, each run only when the one before it passed:
//   build      new LiquidityPoolAsset(assetA, assetB, 30), then the ChangeTrust operation's XDR bytes;
//   decode     Operation.fromXDRObject of those bytes (the indexer's decode path);
//   re_encode  the ChangeTrust operation built again from the decoded line and limit, its bytes compared.
//
// It prints exactly one JSON line on standard output:
//   {"sdkVersion": "...", "steps": [{"step": "build", "passed": true, "error": null}, ...],
//    "roundTripIdentical": true, "builtXdr": "<base64>", "reEncodedXdr": "<base64>"}
// A failed step carries the thrown message as its error. A run that prints no such line (the SDK failed to load) is a
// failure of the run, never read as a rejection. The script is a reproduction kept in the repository, not the
// tool's source; it is run, never imported.

// The SDK is loaded as its namespace: its ESM build (17.2.0 and 17.2.1) provides no default export.
import * as sdk from "@stellar/stellar-sdk";
import { readFileSync } from "node:fs";

const { Asset, LiquidityPoolAsset, Operation, xdr } = sdk;

// The fee of entry 2's pool, in basis points: the protocol's only constant-product fee (LiquidityPoolFeeV18).
const POOL_FEE = 30;

const [code, issuerA, issuerB] = process.argv.slice(2);
if (!code || !issuerA || !issuerB || process.argv.length !== 5) {
  process.stderr.write("usage: node repro.mjs <asset code> <issuer A (lower key bytes)> <issuer B>\n");
  process.exit(2);
}

const sdkPackage = new URL("./node_modules/@stellar/stellar-sdk/package.json", import.meta.url);
const sdkVersion = JSON.parse(readFileSync(sdkPackage, "utf8")).version;

const steps = [];
let built = null;
let decoded = null;
let reEncoded = null;

const step = (name, run) => {
  if (steps.length > 0 && !steps[steps.length - 1].passed) {
    return;
  }
  try {
    run();
    steps.push({ step: name, passed: true, error: null });
  } catch (e) {
    const message = e && typeof e.message === "string" ? e.message : String(e);
    steps.push({ step: name, passed: false, error: message });
  }
};

step("build", () => {
  const pool = new LiquidityPoolAsset(new Asset(code, issuerA), new Asset(code, issuerB), POOL_FEE);
  built = Operation.changeTrust({ asset: pool }).toXDR();
});

step("decode", () => {
  decoded = Operation.fromXDRObject(xdr.Operation.fromXDR(built));
  if (decoded.type !== "changeTrust") {
    throw new Error(`decoded operation type ${decoded.type}, not changeTrust`);
  }
});

step("re_encode", () => {
  reEncoded = Operation.changeTrust({ asset: decoded.line, limit: decoded.limit }).toXDR();
});

// toXDR() gives a Uint8Array in the SDK's ESM build; the comparison is of the bytes.
const roundTripIdentical = built !== null && reEncoded !== null && Buffer.compare(Buffer.from(built), Buffer.from(reEncoded)) === 0;

process.stdout.write(
  JSON.stringify({
    sdkVersion,
    steps,
    roundTripIdentical,
    builtXdr: built === null ? null : Buffer.from(built).toString("base64"),
    reEncodedXdr: reEncoded === null ? null : Buffer.from(reEncoded).toString("base64"),
  }) + "\n",
);
