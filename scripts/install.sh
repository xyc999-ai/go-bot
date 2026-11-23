#!/usr/bin/env bash
# Install the Docker deployment without cloning the source repository.
set -euo pipefail

directory=go-bot
while (($#)); do
  case "$1" in
    --dir) directory="${2:?missing installation directory}"; shift 2 ;;
    -h|--help)
      echo 'Usage: install.sh [--dir PATH]'
      echo 'Prepare local Docker images and deployment files; start with docker compose up -d.'
      exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done
for command in curl docker; do
  command -v "$command" >/dev/null || { echo "Install $command first." >&2; exit 1; }
done
docker compose version >/dev/null
docker info >/dev/null
mkdir -p "$directory"
directory="$(cd "$directory" && pwd -P)"
temporary="$(mktemp -d "$directory/.install-XXXXXX")"
trap 'rm -rf "$temporary"' EXIT
# The override is useful for mirrors and testing a branch before it reaches main.
source_url="${GO_BOT_INSTALL_SOURCE:-https://raw.githubusercontent.com/xyc999-ai/go-bot/main}"
mkdir -p "$temporary/configs" "$directory/configs"
for file in compose.yaml .env.example configs/default.yaml scripts/install.sh; do
  mkdir -p "$temporary/$(dirname "$file")"
  curl --fail --silent --show-error --location --retry 3 \
    "$source_url/$file" --output "$temporary/$file"
  [[ -s "$temporary/$file" ]] || { echo "Downloaded file is empty: $file" >&2; exit 1; }
done
for file in compose.yaml .env.example configs/default.yaml install.sh; do
  target="$directory/$file"
  if [[ -L "$target" || ( -e "$target" && ! -f "$target" ) ]]; then
    echo "Expected a regular deployment file: $target" >&2
    exit 1
  fi
  if [[ ! -f "$target" ]]; then
    from="$file"
    [[ "$file" != install.sh ]] || from=scripts/install.sh
    mv "$temporary/$from" "$target"
    chmod 644 "$target"
    [[ "$file" != install.sh ]] || chmod 755 "$target"
  else
    echo "Kept $target"
  fi
done
if [[ -L "$directory/.env" || ( -e "$directory/.env" && ! -f "$directory/.env" ) ]]; then
  echo 'Choose an installation directory with a regular private .env file.' >&2
  exit 1
fi
if [[ ! -f "$directory/.env" ]]; then
  (umask 077; cp "$directory/.env.example" "$directory/.env"; chmod 600 "$directory/.env")
fi

cd "$directory"
export GO_BOT_ENV_FILE="$directory/.env" GO_BOT_CONFIG_FILE="$directory/configs/default.yaml"
# Do not use Compose's interpolated .env values as application credentials.
# Controller reads the original file. Only fixed Compose wiring uses interpolation.
compose=(docker compose --env-file /dev/null)
controller_image="$("${compose[@]}" config controller | awk '
  /^  controller:/ { selected=1; next }
  selected && /^  [^ ]/ { selected=0 }
  selected && $1 == "image:" { print $2; exit }
')"
[[ -n "$controller_image" ]] || { echo 'Compose must define a Controller image.' >&2; exit 1; }
while IFS= read -r image; do
  docker pull "$image"
done < <("${compose[@]}" config --images)
mounts=(-v "$directory/.env:/profile.env:ro" -v "$directory/configs/default.yaml:/runtime.yaml:ro")
config_args=(--env-file /profile.env --config /runtime.yaml)
runtime_info="$(docker run --rm --pull=never "${mounts[@]}" "$controller_image" check --print-runtime "${config_args[@]}")"
if [[ "${runtime_info%%$'\n'*}" != docker ]]; then
  echo 'install.sh prepares Docker only. Follow the separate ECI guide for cloud deployment.' >&2
  exit 1
fi
docker pull "${runtime_info#*$'\n'}"
echo "Prepared $directory"
printf 'Start with:\n  cd %q\n  docker compose up -d\nThen open http://localhost:8080\n' "$directory"
