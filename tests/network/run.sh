#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

printf 'Network lab run 1/2\n'
"$script_dir/lab.sh"
printf 'Network lab run 2/2\n'
"$script_dir/lab.sh"
printf 'PASS: network lab is repeatable\n'
