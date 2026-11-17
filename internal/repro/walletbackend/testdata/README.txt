Recorded logs of wallet-backend, the evidence fixtures on which the wallet-backend verdict is tested without
containers, each line led by a Docker log timestamp as `docker logs --timestamps` gives it.

prefix-2a0a84ea-run1.log and prefix-2a0a84ea-run2.log are the two runs of commit 2a0a84ea in the deciding test of
2026-11-14, the feasibility run made before this tool (logs/prefix-2a0a84ea-halts.log, run final1, ledger 29):
attempts 1 to 4 logged as "Error ingesting data for ledger 29 (attempt k/5)", then the panic "failed after 5
attempts" and the exit; the restart's run the same.
fixed-2c873353.log is commit 2c873353 in the same run (logs/fixed-2c873353-passes.log): ledger 29 ingested.
The deciding test's own comment lines were dropped; the timestamps are those of each line's time= field.
