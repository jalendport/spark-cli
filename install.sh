#!/bin/sh

set -eu

repository="jalendport/spark-cli"

case "$(uname -s)" in
	Darwin)
		os="darwin"
		;;
	Linux)
		os="linux"
		;;
	*)
		echo "Unsupported operating system: $(uname -s)" >&2
		exit 1
		;;
esac

case "$(uname -m)" in
	x86_64 | amd64)
		arch="amd64"
		;;
	arm64 | aarch64)
		arch="arm64"
		;;
	*)
		echo "Unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

if ! command -v curl >/dev/null 2>&1; then
	echo "curl is required to install spark." >&2
	exit 1
fi

if ! command -v tar >/dev/null 2>&1; then
	echo "tar is required to install spark." >&2
	exit 1
fi

archive="spark_${os}_${arch}.tar.gz"
download_url="https://github.com/${repository}/releases/latest/download/${archive}"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT HUP INT TERM

echo "Downloading ${archive} from the latest spark release..."
curl -fsSL "$download_url" -o "$temporary_directory/$archive"
tar -xzf "$temporary_directory/$archive" -C "$temporary_directory"

if { [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; } || { [ ! -e /usr/local/bin ] && [ -w /usr/local ]; }; then
	install_directory="/usr/local/bin"
	mkdir -p "$install_directory"
else
	install_directory="${HOME}/.local/bin"
	mkdir -p "$install_directory"
fi

cp "$temporary_directory/spark" "$install_directory/spark"
chmod 755 "$install_directory/spark"

echo "Installed spark to ${install_directory}/spark."
