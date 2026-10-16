# pathctl

pathctl keeps `PATH` predictable.

Most ways of managing `PATH` put an ordered list of directories in front of it
and let the order decide which file a command runs. The result then depends on
everything else that edits `PATH` — system startup scripts, package managers,
version managers, the environment a parent process passed down — and on when
each of them runs. When the order changes, the same command quietly runs a
different file.

pathctl separates the two concerns:

- **Directories are only ever appended.** Adding a directory never changes which
  file an existing command runs, so it does not matter when, how often, or in
  what order relative to other tools pathctl runs.
- **Conflicts are decided per command.** `pathctl use <name> <file>` makes
  `<name>` run `<file>`, through a symlink in one directory that pathctl puts
  first on `PATH`. Changing a choice only replaces the symlink, so it applies to
  every process that looks the command up afterwards, without reloading `PATH`.
- **Problems are visible.** `pathctl doctor` lists commands that exist in more
  than one place without a decision, and decisions that do not take effect.

## Install

Download the archive for your platform from the
[releases](https://github.com/vika2603/pathctl/releases) page, or build it:

```sh
go install github.com/vika2603/pathctl/cmd/pathctl@latest
```

Then run `pathctl init` from your shell startup files. If `pathctl` is not on
`PATH` yet at that point, call it by its full path.

**zsh**: in both `~/.zshenv` and `~/.zprofile`:

```sh
eval "$(pathctl init zsh)"
```

`~/.zshenv` covers every zsh, including scripts. On macOS, login shells — what
Terminal and iTerm open by default — then run `path_helper` from
`/etc/zprofile`, which moves the system directories in front of everything
else; running init again from `~/.zprofile` puts the override directory back in
front.

**bash**: in both `~/.bash_profile` (login shells) and `~/.bashrc` (other
interactive shells):

```sh
eval "$(pathctl init bash)"
```

Non-interactive bash reads neither file; it inherits `PATH` from its parent.

Running init more than once is safe: it only moves the override directory to
the front and appends what is missing.

## Usage

```sh
pathctl add ~/.local/bin ~/go/bin     # append directories to PATH
pathctl rm ~/go/bin

pathctl use python3 /opt/homebrew/bin/python3   # python3 runs this file
pathctl unuse python3

pathctl ls        # show the configuration
pathctl doctor    # report conflicts and choices that do not take effect
pathctl edit      # open the configuration in $VISUAL or $EDITOR
```

Shells remember where they found a command. A shell that has already run
`python3` keeps running the file it found until you run `hash -r` (bash) or
`rehash` (zsh); the `pathctl` shell function defined by init does this for the
shell it runs in.

`doctor` exits with status 1 when it finds a problem:

```
make: /usr/bin/make runs; also found:
  ~/.local/bin/make
  choose one with: pathctl use make <file>
```

## Configuration

`$XDG_CONFIG_HOME/pathctl/config` (default `~/.config/pathctl/config`), one
directive per line; lines starting with `#` are comments:

```
dir ~/.local/bin
dir $GOPATH/bin
use python3 /opt/homebrew/bin/python3
```

Paths must be absolute or start with `~` or a variable. `~`, `$VAR` and
`${VAR}` are expanded when the file is used, so one file works on several
machines. Entries that do not exist, or that refer to an unset variable, are
skipped until they resolve. Edits made by pathctl keep comments and blank lines;
if the file is a symlink, the file it points to is written and the link is kept.

The links made by `use` live in `$XDG_DATA_HOME/pathctl/bin` (default
`~/.local/share/pathctl/bin`). `pathctl init` brings that directory in line
with the configuration, so after editing or syncing the file by hand, the next
shell picks up the change. pathctl only manages the symlinks in that directory
and never replaces a regular file there.

## How init works

`pathctl init` prints code that:

- puts the override directory first on `PATH`;
- keeps every other entry in its current order, dropping duplicates and
  directories pathctl appended earlier that are no longer configured;
- appends configured directories that are not on `PATH` yet, and records them
  in `PATHCTL_DIRS`;
- defines a `pathctl` shell function that updates the current shell after
  `add`, `rm`, `edit`, `use` and `unuse`.

It runs in a few milliseconds, so it can run in every shell. Concurrent runs,
such as several terminals opening at once, are serialized with a lock file next
to the override directory.

If something moves entries in front of the override directory after init has
run, a choice made with `use` stops taking effect for commands those entries
also provide. `pathctl doctor` reports it; running init again fixes it.

## Limitation

If the file chosen with `use` is a wrapper that finds the real program by
skipping its own directory and searching `PATH` for the next file with the same
name, it finds the pathctl link and runs itself again. Point `use` at the real
program instead, or make the wrapper compare resolved paths rather than
directories.
