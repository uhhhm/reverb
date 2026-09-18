#!/usr/bin/env bash
# Fails on every vulnerability Reverb's own code path reaches, except those
# listed as accepted below.
#
# govulncheck's own exit status cannot express an exception, and one advisory
# that applies here has no fixed version to upgrade to. Without this filter the
# job is permanently red, which is worse than a narrow, documented exception:
# a red check nobody can make green stops reporting the vulnerabilities that
# can be fixed.
set -euo pipefail

# Accepted, with the reason each one is not actionable. Remove an entry as soon
# as a fixed version exists — the summary below prints the accepted findings on
# every run so they stay visible rather than becoming invisible policy.
#
#   GO-2024-3218 — content censorship via Kademlia DHT abuse in
#   go-libp2p-kad-dht. Reported with no fixed version: upstream treats it as
#   inherent to the public DHT. Reverb only uses the DHT to locate devices in
#   one household and every pairing is authenticated over it, so a poisoned
#   routing table can delay a peer being found, not join or read the library.
ACCEPTED=(GO-2024-3218)

report=$(mktemp)
trap 'rm -f "$report"' EXIT
# govulncheck exits non-zero when it finds something; the JSON is the result,
# so failure here is only interesting when it produced nothing to read.
govulncheck -format json ./... >"$report" || true
if [ ! -s "$report" ]; then
  echo "govulncheck produced no output" >&2
  exit 1
fi

# A finding is "called" when its trace names a function: govulncheck also emits
# import-only and module-only findings, which its own exit status ignores too.
called=$(jq -r --slurp '
  [ .[] | select(has("finding")) | .finding
    | select([.trace[]? | select(has("function"))] | length > 0)
    | .osv ] | unique | .[]' "$report")

accepted_re=$(IFS='|'; echo "${ACCEPTED[*]}")
unexpected=$(printf '%s\n' "$called" | grep -vE "^(${accepted_re})$" | grep -v '^$' || true)

for id in $called; do
  case " ${ACCEPTED[*]} " in
    *" $id "*) echo "accepted: $id (see scripts/govulncheck.sh)" ;;
  esac
done

if [ -n "$unexpected" ]; then
  echo
  echo "govulncheck found vulnerabilities in reachable code:"
  printf '  %s\n' $unexpected
  echo
  govulncheck ./... || true
  exit 1
fi

echo "no unaccepted vulnerabilities in reachable code"
