// Package recipes is the Shape recipe registry: one recipe per seed entry, in seed order, each a value that submits
// its shape's transactions to a network and returns the capture, and that states, through `Intended()`, the
// reproduced values it submits. Its determinism test holds `Intended()` to the committed corpus with no container.
//
// The three recipes are the seed entries, their identity read from internal/model's seed definitions:
// entry 1, a SetOptions adding a signed-payload signer with a 29-byte payload (CAP-0040); entry 2, a pool-share
// ChangeTrust whose two issuers sort one way as text and the other way as key bytes, with the network's refusal of
// the text order as its evidence (CAP-0038); entry 3, a payment from a muxed source to a muxed destination
// (CAP-0027). Every key is derived from a fixed seed and funded by the local friendbot; recipes hold no
// process-global state, because the wallet-backend reproduction runs entry 1's recipe against its own network.
//
// Each recipe's document, `<entry id>.md`, is kept here beside its code: the transactions it
// submits, the CAP and protocol version it declares, and the JSON pointer of each expected value in the decoded form.
//
// It holds recipe.go (the Recipe interface, the registry, and the RPC and Funder interfaces internal/generate's
// client and friendbot satisfy), rpcwire.go (the four RPC methods' results), submit.go (what every
// recipe does with the network), intended.go (the decoded-JSON pointers of Intended()), and one file per recipe:
// signed_payload.go, pool_share.go, muxed.go.
package recipes
