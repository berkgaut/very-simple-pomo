# CLAUDE.md

## pomo -- Pommodoro timer, the unix way

Implementation language: Golang

Primary platform at the moment: macOS

## Architecture

Single binary.

State and configuration stored by default in ~/.pomo

The waiting part detaches from the controlling terminal and turns into a daemon. Controlled via Unix domain socket ~/.pomo/daemon/socket.

Daemon liveness check: attempt to connect to the socket. Successful connection means the daemon is up; connection refused or no such file means it is down.

Daemonization: re-exec the same binary (located via `os.Executable()`) with a hidden `--daemon` flag. stdin/stdout/stderr are redirected to /dev/null and `Setsid` is set in `SysProcAttr` to detach from the controlling terminal. Reason for not using fork(): Go runtime creates multiple threads early.

Daemon lifetime: the daemon exits after the timer fires (or when stopped). No running daemon means no active timer.

CLI talks to the daemon through the socket. If daemon is not running, starts the daemon.

Socket protocol: newline-delimited text commands.

Whenever time has elapsed, pomo runs the notification command and writes a COMPLETE journal entry, then exits.

Journal ~/.pomo/journal — one line per entry. Line format:
`YYYY-mm-ddThh:mm:ss <elapsed_minutes:elapsed_seconds> <COMPLETE|CANCELED> <comment>`

where `elapsed_minutes:elapsed_seconds` is the actual elapsed time at the moment the entry is written.

### Command Line Interface

#### pomo remaining

Print remaining time in minutes:seconds format to standard output.

If timer is not active, exits silently with exit code 127.

#### pomo NN [journal_entry]

With a numeric argument, starts a timer for a given number of minutes.

If timer is already running, cancels existing timer (writes CANCELED journal entry with elapsed time) and starts new one.

Optional extra argument is a journal comment, e.g. `pomo 15 'look at X, timeboxed'`

#### pomo stop

Stops a running timer. Writes a CANCELED journal entry with elapsed time.

#### pomo list

Dumps raw journal entries for the current day.

### Config

TOML configuration file in `~/.pomo/config.toml`.

Initialize config from a provided config file template.

Config options described in the same template.