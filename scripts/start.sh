#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

profile=.env
docker_profile=.env
eci_profile=.env.eci
config_file=configs/default.yaml
init_only=false
launch_options=false
init_options=false
usage() {
  echo "Usage: $0 --init-env [--docker-env-file PATH] [--eci-env-file PATH]"
  echo "       $0 [--env-file PATH] [--config PATH]"
}
while (($#)); do
  case "$1" in
    --env-file) profile="${2:?missing profile path}"; launch_options=true; shift 2 ;;
    --config) config_file="${2:?missing config path}"; launch_options=true; shift 2 ;;
    --docker-env-file) docker_profile="${2:?missing Docker profile path}"; init_options=true; shift 2 ;;
    --eci-env-file) eci_profile="${2:?missing ECI profile path}"; init_options=true; shift 2 ;;
    --init-env) init_only=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if { $init_only && $launch_options; } || { ! $init_only && $init_options; }; then
  usage >&2
  exit 2
fi

if $init_only; then
  docker_profile="$(cd "$(dirname "$docker_profile")" && pwd -P)/$(basename "$docker_profile")"
  eci_profile="$(cd "$(dirname "$eci_profile")" && pwd -P)/$(basename "$eci_profile")"
  if [[ "$docker_profile" == "$eci_profile" || "$docker_profile" -ef "$eci_profile" ]]; then
    echo 'Docker and ECI profiles need distinct paths.' >&2
    exit 2
  fi
  for path in "$docker_profile" "$eci_profile"; do
    if [[ "$(basename "$path")" == .env.example || "$path" -ef .env.example || -L "$path" || ( -e "$path" && ! -f "$path" ) ]]; then
      echo "Choose a private profile file for $path." >&2
      exit 2
    fi
  done
  create_profile() (
    umask 077
    path="$1"
    if [[ -f "$path" ]]; then
      echo "Kept $path"
      exit 0
    fi
    temporary="$(mktemp "$path.tmp.XXXXXX")"
    trap 'unlink "$temporary"' EXIT
    if [[ "$2" == eci ]]; then
      sed 's/^HARNESS_RUNTIME=docker$/HARNESS_RUNTIME=eci/' .env.example > "$temporary"
    else
      cat .env.example > "$temporary"
    fi
    ln "$temporary" "$path"
    echo "Created $path"
  )
  create_profile "$docker_profile" docker
  create_profile "$eci_profile" eci
  exit 0
fi

if [[ ! -f "$profile" ]]; then
  echo "Deployment profile $profile is missing. Run ./scripts/start.sh --init-env first." >&2
  exit 1
fi

profile_dir="$(cd "$(dirname "$profile")" && pwd)"
profile_name="$(basename "$profile")"
profile="$profile_dir/$profile_name"
config_file="$(cd "$(dirname "$config_file")" && pwd)/$(basename "$config_file")"
export GO_BOT_ENV_FILE="$profile" GO_BOT_CONFIG_FILE="$config_file"
compose=(docker compose --env-file /dev/null)
docker info >/dev/null
controller_image="$("${compose[@]}" config controller | awk '
  /^  controller:/ { selected=1; next }
  selected && /^  [^ ]/ { selected=0 }
  selected && $1 == "image:" { print $2; exit }
')"
[[ -n "$controller_image" ]] || { echo 'Compose must define a Controller image.' >&2; exit 1; }
while IFS= read -r image; do
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "Required local image is missing: $image. Prepare it with docker pull or an explicit source build first." >&2
    exit 1
  fi
done < <("${compose[@]}" config --images)
profile_mounts=(-v "$profile:/profile.env:ro" -v "$config_file:/config.yaml:ro")
config_args=(--env-file /profile.env --config /config.yaml)
forwarded=()
environment_keys=()
while IFS='=' read -r key _; do
  if [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] && printenv "$key" >/dev/null; then
    forwarded+=(-e "$key")
    environment_keys+=("$key")
  fi
done < .env.example
runtime_info="$(docker run --rm --pull=never "${profile_mounts[@]}" ${forwarded[@]+"${forwarded[@]}"} \
  "$controller_image" check --print-runtime "${config_args[@]}")"

selected_runtime="${runtime_info%%$'\n'*}"
selected_image="${runtime_info#*$'\n'}"
if [[ "$selected_runtime" == docker ]]; then
  if ! docker image inspect "$selected_image" >/dev/null 2>&1; then
    echo "Host image is missing: $selected_image. Prepare it with docker pull or an explicit source build first." >&2
    exit 1
  fi
fi
# Forward only explicitly set shell variables for this launch. The temporary
# Compose override contains variable names, never credential values.
if ((${#environment_keys[@]})); then
  mkdir -p .cache
  environment_override="$(mktemp "$repo_dir/.cache/start-env.XXXXXX")"
  trap 'rm -f "$environment_override"' EXIT
  {
    printf 'services:\n  controller:\n    environment:\n'
    printf '      %s:\n' "${environment_keys[@]}"
  } > "$environment_override"
  export COMPOSE_FILE="${COMPOSE_FILE:-$repo_dir/compose.yaml}:$environment_override"
fi
"${compose[@]}" up -d --no-build --pull never --wait
published="$("${compose[@]}" port controller 8080)"
published="${published%%$'\n'*}"
published_port="${published##*:}"
if command -v curl >/dev/null 2>&1; then
  for ((attempt = 0; attempt < 30; attempt++)); do
    if curl --fail --silent --max-time 2 "http://127.0.0.1:$published_port/readyz" >/dev/null; then
      echo "Go Bot is ready at http://localhost:$published_port"
      exit 0
    fi
    sleep 1
  done
  "${compose[@]}" logs --tail 20 controller >&2
  exit 1
fi
echo "Open http://localhost:$published_port"
