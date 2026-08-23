#!/usr/bin/env bash

set -Eeuo pipefail
IFS=$'\n\t'
umask 077

readonly SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly ENV_FILE="${SCRIPT_DIR}/.env"

RECONFIGURE=0
NON_INTERACTIVE=0
SKIP_PACKAGE_INSTALL=0
TEMP_ENV=""

log() {
	printf '[install] %s\n' "$*"
}

warn() {
	printf '[install] WARNING: %s\n' "$*" >&2
}

die() {
	printf '[install] ERROR: %s\n' "$*" >&2
	exit 1
}

cleanup() {
	if [[ -n "${TEMP_ENV}" && -f "${TEMP_ENV}" ]]; then
		rm -f -- "${TEMP_ENV}"
	fi
}
trap cleanup EXIT

usage() {
	cat <<'USAGE'
Usage: ./install.sh [options]

Installs host prerequisites when needed, creates a secure .env, builds the
containers, starts PostgreSQL and CrackSMS, runs migrations through bot
startup, and waits for the readiness endpoint.

Options:
  --reconfigure          Back up and rebuild an existing .env.
  --non-interactive      Read configuration from environment variables.
  --skip-package-install Fail instead of installing missing host packages.
  -h, --help             Show this help.

Required in non-interactive mode:
  BOT_TOKEN              Telegram BotFather token.
  INITIAL_ADMIN_IDS      Comma-separated Telegram user IDs.

All other values are generated securely or use .env.example defaults.
USAGE
}

while (($# > 0)); do
	case "$1" in
	--reconfigure)
		RECONFIGURE=1
		;;
	--non-interactive)
		NON_INTERACTIVE=1
		;;
	--skip-package-install)
		SKIP_PACKAGE_INSTALL=1
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown option: $1"
		;;
	esac
	shift
done

if [[ ! -f "${SCRIPT_DIR}/docker-compose.yml" || ! -f "${SCRIPT_DIR}/Dockerfile" ]]; then
	die "run the installer from a complete CrackSMS repository checkout"
fi

have() {
	command -v "$1" >/dev/null 2>&1
}

as_root() {
	if ((EUID == 0)); then
		"$@"
	elif have sudo; then
		sudo "$@"
	else
		die "root access or sudo is required to install host packages"
	fi
}

compose_command_exists() {
	if have docker && docker compose version >/dev/null 2>&1; then
		return 0
	fi
	have docker-compose
}

host_requirements_ready() {
	have docker && have curl && have openssl && compose_command_exists
}

install_host_packages() {
	if ((SKIP_PACKAGE_INSTALL == 1)); then
		die "Docker, Docker Compose, curl, or OpenSSL is missing and package installation was disabled"
	fi
	[[ "$(uname -s)" == "Linux" ]] || die "automatic package installation currently supports Linux only"

	local distro=""
	if [[ -r /etc/os-release ]]; then
		# The distribution-owned file contains simple OS identification fields.
		# shellcheck disable=SC1091
		. /etc/os-release
		distro="${ID:-}"
	fi

	log "Installing missing host prerequisites for ${distro:-Linux}..."
	case "${distro}" in
	ubuntu | debian | linuxmint | pop)
		as_root apt-get update
		as_root apt-get install -y ca-certificates curl openssl
		if ! have docker; then
			as_root apt-get install -y docker.io
		fi
		if ! compose_command_exists; then
			if ! as_root apt-get install -y docker-compose-v2; then
				if ! as_root apt-get install -y docker-compose-plugin; then
					as_root apt-get install -y docker-compose
				fi
			fi
		fi
		;;
	fedora | rhel | centos | rocky | almalinux)
		as_root dnf install -y ca-certificates curl openssl docker docker-compose-plugin
		;;
	alpine)
		as_root apk add --no-cache ca-certificates curl openssl docker docker-cli-compose
		;;
	arch | manjaro)
		as_root pacman -Sy --needed --noconfirm ca-certificates curl openssl docker docker-compose
		;;
	*)
		die "unsupported distribution; install Docker Engine, Docker Compose, curl, and OpenSSL, then rerun with --skip-package-install"
		;;
	esac
}

if ! host_requirements_ready; then
	install_host_packages
fi
host_requirements_ready || die "required host tools are still unavailable after package installation"

start_docker_service() {
	if have systemctl; then
		as_root systemctl enable --now docker
	elif have rc-service; then
		as_root rc-update add docker default >/dev/null 2>&1 || true
		as_root rc-service docker start
	elif have service; then
		as_root service docker start
	fi
}

if ! docker info >/dev/null 2>&1; then
	start_docker_service
fi

DOCKER=()
if docker info >/dev/null 2>&1; then
	DOCKER=(docker)
elif ((EUID != 0)) && have sudo && sudo docker info >/dev/null 2>&1; then
	DOCKER=(sudo docker)
	warn "Docker requires sudo for this session. Add your user to the docker group later if desired."
else
	die "Docker daemon is unavailable or the current user lacks permission"
fi

COMPOSE=()
if "${DOCKER[@]}" compose version >/dev/null 2>&1; then
	COMPOSE=("${DOCKER[@]}" compose)
elif have docker-compose; then
	if [[ "${DOCKER[0]}" == "sudo" ]]; then
		COMPOSE=(sudo docker-compose)
	else
		COMPOSE=(docker-compose)
	fi
else
	die "Docker Compose is unavailable"
fi

env_value() {
	local key="$1"
	local file="${2:-${ENV_FILE}}"
	[[ -f "${file}" ]] || return 0
	awk -v wanted="${key}" '
		index($0, wanted "=") == 1 {
			value = substr($0, length(wanted) + 2)
			sub(/\r$/, "", value)
			print value
			exit
		}
	' "${file}"
}

valid_bot_token() {
	[[ "$1" =~ ^[0-9]{5,}:[A-Za-z0-9_-]{20,}$ ]]
}

valid_admin_ids() {
	[[ "$1" =~ ^[0-9]+(,[0-9]+)*$ ]]
}

valid_url() {
	[[ -z "$1" || "$1" =~ ^https://[^[:space:]#]+$ ]]
}

valid_panel_key() {
	local decoded_length
	if ! decoded_length="$(printf '%s' "$1" | openssl base64 -d -A 2>/dev/null | wc -c | tr -d '[:space:]')"; then
		return 1
	fi
	[[ "${decoded_length}" == "32" ]]
}

validate_env_file() {
	local token admins postgres_password metrics panel_key http_addr
	token="$(env_value BOT_TOKEN "$1")"
	admins="$(env_value INITIAL_ADMIN_IDS "$1")"
	postgres_password="$(env_value POSTGRES_PASSWORD "$1")"
	metrics="$(env_value METRICS_TOKEN "$1")"
	panel_key="$(env_value PANEL_CONFIG_KEY "$1")"
	http_addr="$(env_value HTTP_ADDR "$1")"

	valid_bot_token "${token}" || return 1
	valid_admin_ids "${admins}" || return 1
	[[ "${postgres_password}" =~ ^[A-Za-z0-9_-]{32,}$ ]] || return 1
	[[ "${metrics}" =~ ^[A-Za-z0-9_-]{32,}$ ]] || return 1
	valid_panel_key "${panel_key}" || return 1
	[[ "${http_addr}" == ":8080" ]] || return 1
}

old_value() {
	env_value "$1" "${ENV_FILE}"
}

configured_value() {
	local key="$1"
	local fallback="$2"
	local from_environment="${!key-}"
	local from_file=""
	if [[ -f "${ENV_FILE}" ]]; then
		from_file="$(old_value "${key}")"
	fi
	if [[ -n "${from_environment}" ]]; then
		printf '%s' "${from_environment}"
	elif [[ -n "${from_file}" ]]; then
		printf '%s' "${from_file}"
	else
		printf '%s' "${fallback}"
	fi
}

prompt_secret() {
	local label="$1"
	local current="$2"
	local entered=""
	if [[ -n "${current}" ]]; then
		read -r -s -p "${label} [press Enter to keep existing]: " entered
	else
		read -r -s -p "${label}: " entered
	fi
	printf '\n' >&2
	if [[ -n "${entered}" ]]; then
		printf '%s' "${entered}"
	else
		printf '%s' "${current}"
	fi
}

prompt_text() {
	local label="$1"
	local current="$2"
	local entered=""
	if [[ -n "${current}" ]]; then
		read -r -p "${label} [${current}]: " entered
	else
		read -r -p "${label} [optional]: " entered
	fi
	if [[ -n "${entered}" ]]; then
		printf '%s' "${entered}"
	else
		printf '%s' "${current}"
	fi
}

create_environment() {
	local old_token old_admins
	local token admins channel_url number_bot_url developer_url support_url timezone
	local postgres_password metrics_token panel_key

	old_token="$(configured_value BOT_TOKEN "")"
	old_admins="$(configured_value INITIAL_ADMIN_IDS "")"

	if ((NON_INTERACTIVE == 1)); then
		token="${old_token}"
		admins="${old_admins}"
	else
		while true; do
			token="$(prompt_secret "Telegram BotFather token" "${old_token}")"
			valid_bot_token "${token}" && break
			warn "enter a valid Telegram bot token"
		done
		while true; do
			admins="$(prompt_text "Initial admin user IDs (comma-separated)" "${old_admins}")"
			admins="$(printf '%s' "${admins}" | tr -d '[:space:]')"
			valid_admin_ids "${admins}" && break
			warn "enter one or more positive Telegram user IDs separated by commas"
		done
	fi

	valid_bot_token "${token}" || die "BOT_TOKEN is required and invalid in non-interactive mode"
	admins="$(printf '%s' "${admins}" | tr -d '[:space:]')"
	valid_admin_ids "${admins}" || die "INITIAL_ADMIN_IDS is required and invalid in non-interactive mode"

	channel_url="$(configured_value CHANNEL_URL "")"
	number_bot_url="$(configured_value NUMBER_BOT_URL "")"
	developer_url="$(configured_value DEVELOPER_URL "")"
	support_url="$(configured_value SUPPORT_URL "")"
	timezone="$(configured_value TIMEZONE "Asia/Karachi")"

	if ((NON_INTERACTIVE == 0)); then
		channel_url="$(prompt_text "Telegram channel URL" "${channel_url}")"
		number_bot_url="$(prompt_text "Number bot URL" "${number_bot_url}")"
		developer_url="$(prompt_text "Developer URL" "${developer_url}")"
		support_url="$(prompt_text "Support URL" "${support_url}")"
		timezone="$(prompt_text "Timezone" "${timezone}")"
	fi

	valid_url "${channel_url}" || die "CHANNEL_URL must be blank or use https://"
	valid_url "${number_bot_url}" || die "NUMBER_BOT_URL must be blank or use https://"
	valid_url "${developer_url}" || die "DEVELOPER_URL must be blank or use https://"
	valid_url "${support_url}" || die "SUPPORT_URL must be blank or use https://"
	[[ "${timezone}" =~ ^[A-Za-z0-9_+./-]+$ ]] || die "TIMEZONE contains invalid characters"

	postgres_password="$(configured_value POSTGRES_PASSWORD "")"
	metrics_token="$(configured_value METRICS_TOKEN "")"
	panel_key="$(configured_value PANEL_CONFIG_KEY "")"
	if [[ ! "${postgres_password}" =~ ^[A-Za-z0-9_-]{32,}$ ]]; then
		postgres_password="$(openssl rand -hex 32)"
	fi
	if [[ ! "${metrics_token}" =~ ^[A-Za-z0-9_-]{32,}$ ]]; then
		metrics_token="$(openssl rand -hex 32)"
	fi
	if ! valid_panel_key "${panel_key}"; then
		panel_key="$(openssl rand -base64 32 | tr -d '\n')"
	fi

	TEMP_ENV="$(mktemp "${SCRIPT_DIR}/.env.tmp.XXXXXX")"
	chmod 600 "${TEMP_ENV}"
	{
		printf 'BOT_TOKEN=%s\n' "${token}"
		printf 'POSTGRES_PASSWORD=%s\n' "${postgres_password}"
		printf 'DATABASE_URL=postgres://cracksms:%s@postgres:5432/cracksms?sslmode=disable\n' "${postgres_password}"
		printf 'INITIAL_ADMIN_IDS=%s\n' "${admins}"
		printf 'HTTP_ADDR=:8080\n'
		printf 'METRICS_TOKEN=%s\n' "${metrics_token}"
		printf 'PANEL_CONFIG_KEY=%s\n' "${panel_key}"
		printf 'TIMEZONE=%s\n' "${timezone}"
		printf 'HOLD_DURATION=%s\n' "$(configured_value HOLD_DURATION "20m")"
		printf 'SAME_USER_REUSE_COOLDOWN=%s\n' "$(configured_value SAME_USER_REUSE_COOLDOWN "24h")"
		printf 'PANEL_REFRESH_INTERVAL=%s\n' "$(configured_value PANEL_REFRESH_INTERVAL "30s")"
		printf 'CHILD_REFRESH_INTERVAL=%s\n' "$(configured_value CHILD_REFRESH_INTERVAL "15s")"
		printf 'DELIVERY_WORKERS=%s\n' "$(configured_value DELIVERY_WORKERS "8")"
		printf 'WEBHOOK_WORKERS=%s\n' "$(configured_value WEBHOOK_WORKERS "4")"
		printf 'PANEL_WORKERS_LIMIT=%s\n' "$(configured_value PANEL_WORKERS_LIMIT "50")"
		printf 'TELEGRAM_SEND_RATE=%s\n' "$(configured_value TELEGRAM_SEND_RATE "25")"
		printf 'LOG_LEVEL=%s\n' "$(configured_value LOG_LEVEL "info")"
		printf 'CHANNEL_URL=%s\n' "${channel_url}"
		printf 'NUMBER_BOT_URL=%s\n' "${number_bot_url}"
		printf 'DEVELOPER_URL=%s\n' "${developer_url}"
		printf 'SUPPORT_URL=%s\n' "${support_url}"
	} >"${TEMP_ENV}"

	validate_env_file "${TEMP_ENV}" || die "generated configuration did not pass validation"
	if [[ -f "${ENV_FILE}" ]]; then
		local backup_file
		backup_file="${ENV_FILE}.backup.$(date -u +%Y%m%dT%H%M%SZ).$$"
		cp -p -- "${ENV_FILE}" "${backup_file}"
		log "Existing configuration backed up to ${backup_file##*/}"
	fi
	mv -f -- "${TEMP_ENV}" "${ENV_FILE}"
	TEMP_ENV=""
	chmod 600 "${ENV_FILE}"
	log "Secure configuration written to .env"
}

if [[ -f "${ENV_FILE}" && ${RECONFIGURE} -eq 0 ]]; then
	validate_env_file "${ENV_FILE}" || die "existing .env is incomplete; rerun with --reconfigure"
	chmod 600 "${ENV_FILE}"
	log "Using the existing validated .env (use --reconfigure to change it)"
else
	create_environment
fi

cd -- "${SCRIPT_DIR}"
log "Validating Docker Compose configuration..."
"${COMPOSE[@]}" config --quiet

log "Building and starting PostgreSQL and CrackSMS..."
if ! "${COMPOSE[@]}" up --build --detach --remove-orphans; then
	"${COMPOSE[@]}" ps || true
	die "Docker Compose startup failed"
fi

log "Waiting for PostgreSQL and automatic migrations..."
postgres_ready=0
for _ in {1..45}; do
	if "${COMPOSE[@]}" exec -T postgres pg_isready -U cracksms -d cracksms >/dev/null 2>&1; then
		postgres_ready=1
		break
	fi
	sleep 2
done
if ((postgres_ready == 0)); then
	"${COMPOSE[@]}" logs --tail=120 postgres || true
	die "PostgreSQL did not become ready within 90 seconds"
fi

log "Waiting for the bot readiness endpoint..."
bot_ready=0
for _ in {1..60}; do
	if curl --fail --silent --show-error --max-time 3 http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
		bot_ready=1
		break
	fi
	sleep 2
done
if ((bot_ready == 0)); then
	"${COMPOSE[@]}" ps || true
	"${COMPOSE[@]}" logs --tail=160 bot || true
	die "the bot did not become ready within 120 seconds; review the logs above"
fi

"${COMPOSE[@]}" ps
printf '\nCrackSMS installation completed successfully.\n'
printf 'Readiness: http://127.0.0.1:8080/readyz\n'
printf 'Logs:      '
printf '%q ' "${COMPOSE[@]}" logs --follow bot
printf '\n'
printf 'Admin:     open the bot in Telegram and send /start or /admin\n'
