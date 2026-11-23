#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

profile=.env.eci
args=()
if [[ "${1:-}" == cache ]]; then
  shift
fi
while (($#)); do
  case "$1" in
    --env-file) profile="${2:?missing profile path}"; shift 2 ;;
    --config|--operation) args+=("$1" "${2:?missing option value}"); shift 2 ;;
    -h|--help)
      echo "Usage: $0 [cache] [--env-file PATH] [--config PATH] [--operation NAME]"
      exit 0
      ;;
    *) echo "Unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done

if [[ ! -f "$profile" ]]; then
  echo "Deployment profile $profile is missing. Run ./scripts/start.sh --init-env first." >&2
  exit 1
fi

if [[ "$profile" -ef .env.example ]]; then
  echo 'Use a private environment profile instead of .env.example.' >&2
  exit 1
fi

controller_image="$(docker compose --env-file /dev/null config controller | awk '
  /^  controller:/ { selected=1; next }
  selected && /^  [^ ]/ { selected=0 }
  selected && $1 == "image:" { print $2; exit }
')"
[[ -n "$controller_image" ]] || { echo 'Compose must define a Controller image.' >&2; exit 1; }
if ! docker image inspect "$controller_image" >/dev/null 2>&1; then
  echo "Required local image is missing: $controller_image. Pull it before preparing ECI." >&2
  exit 1
fi
profile_dir="$(cd "$(dirname "$profile")" && pwd)"
profile_name="$(basename "$profile")"
mkdir -p .cache/buildeci
# Only the profile directory, operation state, and optional tuning are needed.
mounts=(-v "$profile_dir:/profile" -v "$repo_dir/.cache/buildeci:/state")
forwarded_args=()
for ((i=0; i<${#args[@]}; i+=2)); do
  if [[ "${args[i]}" == --config ]]; then
    tuning="$(cd "$(dirname "${args[i+1]}")" && pwd)/$(basename "${args[i+1]}")"
    mounts+=(-v "$tuning:/buildeci.yaml:ro")
    forwarded_args+=(--config /buildeci.yaml)
  else
    forwarded_args+=("${args[i]}" "${args[i+1]}")
  fi
done
cache_id="$(docker run --rm --pull=never --user "$(id -u):$(id -g)" "${mounts[@]}" \
  --entrypoint /usr/local/bin/buildeci "$controller_image" cache \
  --env-file "/profile/$profile_name" --state-dir /state --write-profile --output cache-id \
  ${forwarded_args[@]+"${forwarded_args[@]}"})"
printf 'Saved ECI_IMAGE_CACHE_ID=%s to %s\n' "$cache_id" "$profile"
