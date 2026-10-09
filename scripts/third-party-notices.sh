#!/bin/sh
# Writes the license text of every third-party module that ships in the aigw
# binary, on any of the release OSes, to the file named by $1. Every module
# aigw ships is MIT, BSD or Apache-2.0, and each requires its notice to travel
# with binary distributions.
set -eu

out=${1:?usage: third-party-notices.sh OUTPUT}
main=$(go list -m)

mods=$(for os in linux darwin windows; do
	GOOS=$os GOARCH=amd64 go list -deps -f '{{with .Module}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' .
done | grep -v "^$main " | sort -u)

{
	echo "aigw includes the following third-party software."
	echo
	echo "$mods" | while read -r path version dir; do
		echo "================================================================================"
		echo "$path $version"
		echo "================================================================================"
		found=0
		for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE*; do
			[ -f "$f" ] || continue
			cat "$f"
			echo
			found=1
		done
		if [ "$found" -eq 0 ]; then
			echo "no license file found in $path" >&2
			exit 1
		fi
	done
} > "$out"
