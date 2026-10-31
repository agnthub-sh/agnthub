#!/usr/bin/env bash
# Runs the end-to-end suite in Docker, one container per Harness.
#
#   e2e/run.sh [-update] [harness...]      (default: every Harness)
#
# Each Harness image (e2e/Dockerfile, target e2e-<harness>) holds that Harness's
# CLI at the version pinned in the Dockerfile, plus agnthub and the e2e test
# binary built from this checkout. The container runs without a network: the
# fixture repositories are served over HTTPS on its loopback interface and every
# Harness check is offline. testdata is mounted read-only unless -update.
#
# Environment:
#   E2E_BUILD_ARGS  extra docker build arguments, e.g.
#                   "--build-arg CLAUDE_VERSION=latest --pull --no-cache"
#   E2E_LOG_DIR     also write each Harness's test output to <dir>/<harness>.log
set -euo pipefail

cd "$(dirname "$0")/.."
all=(claude opencode codex copilot cursor gemini omp)

update=()
mount=ro
if [[ "${1:-}" == "-update" ]]; then
	update=(-update)
	mount=rw
	shift
fi
harnesses=("$@")
if [[ ${#harnesses[@]} -eq 0 ]]; then
	harnesses=("${all[@]}")
fi
for harness in "${harnesses[@]}"; do
	if [[ ! " ${all[*]} " == *" ${harness} "* ]]; then
		echo "unknown harness ${harness}; choose from: ${all[*]}" >&2
		exit 2
	fi
done

# GitHub Actions folds each image build into a group.
group() { if [[ -n "${GITHUB_ACTIONS:-}" ]]; then echo "::group::$*"; fi; }
endgroup() { if [[ -n "${GITHUB_ACTIONS:-}" ]]; then endgroup; fi; }

read -r -a build_args <<<"${E2E_BUILD_ARGS:-}"
declare -A outcome
failed=0
for harness in "${harnesses[@]}"; do
	image="agnthub-e2e:${harness}"
	group "build ${image}"
	if ! docker build --file e2e/Dockerfile --target "e2e-${harness}" --tag "${image}" \
		"${build_args[@]}" .; then
		endgroup
		outcome[$harness]="FAIL (image build)"
		failed=1
		continue
	fi
	endgroup
	log=/dev/null
	if [[ -n "${E2E_LOG_DIR:-}" ]]; then
		mkdir -p "${E2E_LOG_DIR}"
		log="${E2E_LOG_DIR}/${harness}.log"
	fi
	if docker run --rm --network none \
		--volume "${PWD}/e2e/testdata:/e2e/testdata:${mount}" \
		"${image}" -test.v -test.count=1 "${update[@]}" 2>&1 | tee "${log}"; then
		outcome[$harness]="PASS"
	else
		outcome[$harness]="FAIL"
		failed=1
	fi
done

echo
echo "agnthub e2e summary"
for harness in "${harnesses[@]}"; do
	printf '  %-9s %s\n' "${harness}" "${outcome[$harness]}"
done
exit "${failed}"
