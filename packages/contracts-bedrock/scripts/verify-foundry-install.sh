#!/usr/bin/env bash

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )
CONTRACTS_BASE=$(dirname "$SCRIPT_DIR")
MONOREPO_BASE=$(dirname "$(dirname "$CONTRACTS_BASE")")
VERSIONS_FILE="${MONOREPO_BASE}/versions.json"

if ! command -v forge &> /dev/null
then
  # shellcheck disable=SC2006
  echo "Is Foundry not installed? Consider installing via pnpm install:foundry" >&2
  exit 1
fi

# Check VERSIONS_FILE has expected foundry property
if ! jq -e '.foundry' "$VERSIONS_FILE" &> /dev/null; then
  echo "'foundry' is missing from $VERSIONS_FILE" >&2
  exit 1
fi

# Extract the expected foundry version from versions.json
EXPECTED_VERSION_STR=$(jq -r '.foundry' "$VERSIONS_FILE")
if [ -z "$EXPECTED_VERSION_STR" ]; then
  echo "Unable to extract Foundry version string from $VERSIONS_FILE" >&2
  exit 1
fi

# Extract timestamp from version string
# The "cut -c 1-19" truncates the nanoseconds and "Z" for Zulu time as it's not supported by MacOS's default date util
EXPECTED_TIMESTAMP=$(echo "$EXPECTED_VERSION_STR" | awk -F'[()]' '{print $2}' | awk '{print $2}' | cut -c 1-19)

# Convert the expected timestamp to Unix epoch time
EXPECTED_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%S" "$EXPECTED_TIMESTAMP" +%s)

# Extract the installed forge version and timestamp
INSTALLED_VERSION_STR=$(forge --version)

# Extract timestamp from version string
# The "cut -c 1-19" truncates the nanoseconds and "Z" for Zulu time as it's not supported by MacOS's default date util
INSTALLED_TIMESTAMP=$(echo "$INSTALLED_VERSION_STR" | awk -F'[()]' '{print $2}' | awk '{print $2}' | cut -c 1-19)

# Convert the installed timestamp to Unix epoch time
INSTALLED_EPOCH=$(date -j -f "%Y-%m-%dT%H:%M:%S" "$INSTALLED_TIMESTAMP" +%s)

# Compare the installed timestamp with the expected timestamp
if [ "$INSTALLED_EPOCH" -eq "$EXPECTED_EPOCH" ]; then
  echo "Foundry version matches the expected version."
elif [ "$INSTALLED_EPOCH" -gt "$EXPECTED_EPOCH" ]; then
  echo "Foundry version is newer than the expected version."
else
  echo "Installed Foundry version is less than expected version. Run pnpm update:foundry to update to expected version." >&2
  echo "Installed: $INSTALLED_VERSION_STR"
  echo "Expected:  $EXPECTED_VERSION_STR"
  exit 1
fi
