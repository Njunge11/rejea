# shellcheck shell=sh
# The look of the scripts of this folder: the banner, the colours, the
# symbols and the progress line. A script reads this file with ".".
#
# The rules are those of the Command Line Interface Guidelines
# (https://clig.dev): colour and movement only when the output is a
# terminal, no colour when NO_COLOR is set, and a log behind a progress
# line that shows when its command fails.

# The scripts that read this file use each value below.
# shellcheck disable=SC2034

ui_terminal=false
if [ -t 1 ] && [ "${TERM:-dumb}" != "dumb" ]; then
	ui_terminal=true
fi

ui_esc="$(printf '\033')"
if [ "$ui_terminal" = true ] && [ -z "${NO_COLOR:-}" ]; then
	ui_reset="${ui_esc}[0m" ui_bold="${ui_esc}[1m" ui_dim="${ui_esc}[2m" ui_under="${ui_esc}[4m"
	ui_green="${ui_esc}[32m" ui_red="${ui_esc}[31m" ui_yellow="${ui_esc}[33m" ui_cyan="${ui_esc}[36m"
else
	ui_reset="" ui_bold="" ui_dim="" ui_under=""
	ui_green="" ui_red="" ui_yellow="" ui_cyan=""
fi

# A terminal that cannot show these characters gets letters.
case "$(locale charmap 2>/dev/null || true)" in
UTF-8 | utf8)
	ui_unicode=true ui_yes="✓" ui_no="✗" ui_dot="•"
	;;
*)
	ui_unicode=false ui_yes="ok" ui_no="x" ui_dot="-"
	;;
esac

# ui_banner prints the name of the project. The six rows go from light
# cyan to blue.
ui_banner() {
	if [ "$ui_terminal" = false ] || [ "$ui_unicode" = false ]; then
		echo "Rejea: RAG engine for PDF and Word documents"
		return 0
	fi
	ui_row=0
	echo
	while IFS= read -r ui_line; do
		ui_shade=""
		if [ -n "$ui_reset" ]; then
			case "$ui_row" in
			0) ui_shade="${ui_esc}[38;5;51m" ;; 1) ui_shade="${ui_esc}[38;5;45m" ;;
			2) ui_shade="${ui_esc}[38;5;39m" ;; 3) ui_shade="${ui_esc}[38;5;33m" ;;
			4) ui_shade="${ui_esc}[38;5;27m" ;; *) ui_shade="${ui_esc}[38;5;21m" ;;
			esac
		fi
		echo "  ${ui_shade}${ui_line}${ui_reset}"
		ui_row=$((ui_row + 1))
	done <<'EOF'
██████╗ ███████╗     ██╗███████╗ █████╗
██╔══██╗██╔════╝     ██║██╔════╝██╔══██╗
██████╔╝█████╗       ██║█████╗  ███████║
██╔══██╗██╔══╝  ██   ██║██╔══╝  ██╔══██║
██║  ██║███████╗╚█████╔╝███████╗██║  ██║
╚═╝  ╚═╝╚══════╝ ╚════╝ ╚══════╝╚═╝  ╚═╝
EOF
	echo "  ${ui_dim}RAG engine for PDF and Word documents${ui_reset}"
}

# ui_time <seconds> prints a time, such as "1m 12s".
ui_time() {
	if [ "$1" -ge 60 ]; then
		echo "$(($1 / 60))m $(($1 % 60))s"
	else
		echo "${1}s"
	fi
}

# ui_step <number> <of> <title> starts a step.
ui_step() {
	ui_step_start="$(date +%s)"
	echo
	echo "${ui_bold}${ui_cyan}[$1/$2]${ui_reset} ${ui_bold}$3${ui_reset}"
}

# ui_ok <text>... prints one line with a mark before each text.
ui_ok() {
	ui_text=""
	for ui_item in "$@"; do
		ui_text="$ui_text   ${ui_green}${ui_yes}${ui_reset} $ui_item"
	done
	echo " ${ui_text#  }"
}

# ui_step_done <text>... is ui_ok with the time of the step at the end.
ui_step_done() {
	ui_text=""
	for ui_item in "$@"; do
		ui_text="$ui_text   ${ui_green}${ui_yes}${ui_reset} $ui_item"
	done
	echo " ${ui_text#  }   ${ui_dim}$(ui_time $(($(date +%s) - ui_step_start)))${ui_reset}"
}

# ui_note <text> prints a line that is less important.
ui_note() { echo "  ${ui_dim}$1${ui_reset}"; }

# ui_warn <text> prints a warning: the work continues.
ui_warn() { echo "  ${ui_yellow}!${ui_reset} $1"; }

# ui_fault <fault> <correction> prints a fault and what to do about it.
ui_fault() {
	echo "  ${ui_red}${ui_no}${ui_reset} $1" >&2
	echo "    ${ui_bold}To correct:${ui_reset} $2" >&2
}

# ui_watch <title> <log file> <process> [command...] shows that a process in
# the background works: in a terminal one line that moves, with the time and
# the last line of the log, if not a line each 30 seconds.
#
# The line shows the last line of the log. If ui_detail has the name of a
# function, the line shows what that function prints.
#
# With a command, the watch ends with success when the command succeeds,
# and with a failure when the process stops first. With no command, it ends
# when the process stops, with the result of the process. CTRL-C stops the
# process.
ui_watch() {
	ui_title="$1" ui_log="$2" ui_pid="$3"
	shift 3
	ui_start="$(date +%s)" ui_tick=0 ui_result=""
	trap 'kill "$ui_pid" 2>/dev/null; printf "\n"; exit 130' INT TERM
	while [ -z "$ui_result" ]; do
		if [ $# -gt 0 ] && "$@"; then
			ui_result=0
		elif ! kill -0 "$ui_pid" 2>/dev/null; then
			ui_result=1
			if [ $# -eq 0 ]; then
				wait "$ui_pid" && ui_result=0
			fi
		else
			ui_tick=$((ui_tick + 1))
			ui_spent=$(($(date +%s) - ui_start))
			ui_last=""
			if [ -n "${ui_detail:-}" ]; then
				ui_last="$("$ui_detail")"
			fi
			if [ "$ui_terminal" = true ]; then
				# The line must not be wider than the terminal.
				ui_room=$(($(tput cols 2>/dev/null || echo 80) - ${#ui_title} - 20))
				if [ "$ui_room" -lt 1 ]; then
					ui_room=1
				fi
				if [ -z "${ui_detail:-}" ]; then
					ui_last="$(grep -v 'GET /health' "$ui_log" 2>/dev/null | tail -n 1 | tr -d '\r' | sed "s/${ui_esc}\[[0-9;]*[A-Za-z]//g" | cut -c "1-$ui_room")"
				fi
				case $((ui_tick % 4)) in 0) ui_spin="|" ;; 1) ui_spin="/" ;; 2) ui_spin="-" ;; *) ui_spin="\\" ;; esac
				printf '\r%s[K  %s%s%s %s %s%s  %s%s' "$ui_esc" "$ui_cyan" "$ui_spin" "$ui_reset" "$ui_title" "$ui_dim" "$(ui_time "$ui_spent")" "$ui_last" "$ui_reset"
				sleep 0.25
			else
				if [ $((ui_tick % 30)) -eq 0 ]; then
					echo "  $ui_title: $(ui_time "$ui_spent")${ui_last:+, $ui_last}"
				fi
				sleep 1
			fi
		fi
	done
	trap - INT TERM
	if [ "$ui_terminal" = true ]; then
		printf '\r%s[K' "$ui_esc"
	fi
	return "$ui_result"
}

# ui_log_end <log file> prints the end of a log, for a command that failed.
ui_log_end() {
	echo "    ${ui_dim}The end of $1:${ui_reset}" >&2
	grep -v 'GET /health' "$1" | tail -n 15 | sed 's/^/      /' >&2
}
