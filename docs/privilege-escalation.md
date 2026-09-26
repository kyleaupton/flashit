# Privilege escalation in FlashIt

Writing to a raw disk requires administrator rights on macOS, Linux and
Windows. This document describes how FlashIt gets those rights on each OS
and how it limits what they can be used for.

## Design

- The app runs unprivileged on every OS.
- A separate helper does the disk work. The OS asks the user for
  permission before the helper gets any rights.
- The helper validates every request itself. It does not rely on checks
  the app has already made.
- The helper never opens a file by path. The app opens the ISO and passes
  the open file to the helper, so the helper can only read what the user
  could already read.
- Only the app that started the helper can connect to it.

## Requests

The app and helper exchange JSON lines. The helper accepts these requests,
one at a time:

| Request       | Effect                                                         |
| ------------- | -------------------------------------------------------------- |
| `ping`        | Confirms the helper is running and the protocol versions match |
| `write_image` | Writes an image to a whole disk (Linux ISOs)                   |
| `format_disk` | Creates a single FAT32 partition (Windows ISOs)                |
| `unmount`     | Unmounts every volume on a disk                                |
| `eject`       | Unmounts and ejects a disk                                     |
| `cancel`      | Stops the current request                                      |

There is no request to run a command, open a path or write at an offset.

## Validation

The helper refuses a request if:

- the target is not a whole disk;
- the disk is not removable (USB or SD). Internal disks, virtual disks and
  disk images are refused;
- the disk holds the running system: the OS volume, the boot partition, or
  swap or a page file. If the helper cannot determine which disks those
  are, it refuses every request;
- the image is larger than the disk;
- the volume label contains characters other than letters, digits, spaces,
  `_` and `-`;
- the filesystem is not FAT32.

Validation runs before the OS permission prompt where possible, so a
refused request does not prompt the user.

## macOS

|         |                                                                                               |
| ------- | --------------------------------------------------------------------------------------------- |
| Helper  | Bundled in `FlashIt.app`, started as a child of the app                                       |
| Runs as | The user, unprivileged                                                                        |
| Prompt  | Password sheet, once per flash, immediately before the erase                                  |
| Channel | A socketpair inherited from the app. There is no socket file, so no other process can connect |
| Image   | Passed as an open file over the socket                                                        |

The helper holds no rights of its own and runs as the user. It is a
separate process so that macOS shares the protocol and validation code with
the other hosts, and so that the authorization that allows opening the disk
never exists in the process running the UI.

For each flash:

1. The helper validates the target.
2. It requests the right to open that disk for writing. macOS shows the
   password sheet.
3. macOS's `authopen` tool opens the disk and returns the open disk to the
   helper, which writes to it.

The helper builds the disk path for `authopen` from its own validated data,
not from the request. The authorization is destroyed as soon as the disk is
open, because until then it allows opening any path as root.

Formatting, unmounting and ejecting use `diskutil`, which does not require
administrator rights on removable disks.

macOS also asks once for access to removable volumes (a privacy
permission, separate from the password). If the user denies it, the helper
reports that to the app.

## Linux

|         |                                                                                         |
| ------- | --------------------------------------------------------------------------------------- |
| Helper  | `/usr/libexec/flashit/flashit-helper`, installed by the `.deb` or `.rpm`, owned by root |
| Runs as | root                                                                                    |
| Prompt  | polkit, once per session, which is normally once per flash                              |
| Channel | A unix socket in a directory only the user can access                                   |
| Image   | Passed as an open file over the socket                                                  |

The app starts the helper with `pkexec`. The polkit action
`dev.kyleupton.flashit.helper` requires an administrator password.

Packaged builds start the helper only from its installed path, and only if
the helper and every directory above it are owned by root and not writable
by anyone else. Without this check, another program running as the user
could replace the helper and have it run as root under FlashIt's prompt.

When the app connects, the helper asks the kernel for the caller's user ID,
which must match the user who approved the prompt.

The helper exits when the app disconnects or after 60 seconds idle. During
a long copy the app pings it every 20 seconds, so it is still running to
eject the disk.

As root, the helper partitions and formats (`parted`, `mkfs.vfat`), writes
images, mounts the new FAT32 volume for the app to copy files to, and
unmounts and ejects. It removes a mount directory only if it created it and
the directory is not a symlink.

FlashIt ships only as `.deb` and `.rpm`. AppImage, Flatpak and Snap cannot
install a root-owned helper or a polkit policy.

## Windows

|         |                                                                                         |
| ------- | --------------------------------------------------------------------------------------- |
| Helper  | `FlashIt.exe`, started a second time with `--privileged-helper`                         |
| Runs as | Administrator                                                                           |
| Prompt  | UAC, once per flash. It shows "Unknown publisher" because the Windows build is unsigned |
| Channel | A named pipe with a random name                                                         |
| Image   | The helper copies the app's open file handle, read-only                                 |

Other processes can see and try to use named pipes, so the helper and the
app each verify the other:

- The helper checks that the process ID it was given is its actual parent,
  and that the parent started first. A reused process ID fails this check.
- The helper creates the pipe so that creation fails if the name already
  exists. It rejects remote connections and grants access only to the
  app's user account.
- On each connection, the helper checks the connecting process is its
  parent. If not, it exits.
- The app checks that the process serving the pipe is the one it launched.
  A pipe created by another process is refused.

Named pipes cannot carry open files, so the app sends the ISO's handle
number and the helper copies the handle out of the app with read-only
access. The helper checks it is a regular file of the expected size. The
helper's log file is passed the same way, so the helper never writes to a
path chosen by another process.

Before writing, formatting or ejecting, the helper locks and dismounts
every volume on the disk. If a volume is in use, it retries for five
seconds and then reports the disk as busy.

The helper formats with go-diskfs, because Windows' own tools cannot create
FAT32 volumes larger than 32 GB. It does not run `diskpart`, PowerShell or
any other program.

This replaced an older C helper whose pipe accepted any client, which only
refused disk 0, and which opened the ISO by path as administrator.

Microsoft does not treat UAC as a security boundary between programs
running as the same user. These checks protect against other users, remote
access and mistakes, not against malware already running as the user.

## Comparison

|                  | macOS                    | Linux                               | Windows                 |
| ---------------- | ------------------------ | ----------------------------------- | ----------------------- |
| Helper location  | App bundle               | `/usr/libexec/flashit/`, root-owned | Same exe as the app     |
| Runs as          | User                     | root                                | Administrator           |
| Prompt           | Password sheet per flash | polkit per session                  | UAC per flash           |
| Privileged scope | One open disk            | Whole helper process                | Whole helper process    |
| Channel          | Inherited socketpair     | Unix socket, private directory      | Named pipe, random name |
| Accepted caller  | Parent only              | Same user as the prompt             | Parent process only     |
| Image transfer   | Open file over socket    | Open file over socket               | Handle copied by helper |
| Formatter        | `diskutil`               | `parted`, `mkfs.vfat`               | go-diskfs               |
| Lifetime         | App lifetime             | Session, 60 s idle                  | One session             |

## Accepted limits

- The Windows build is unsigned. Smart App Control blocks it, SmartScreen
  warns on first run and UAC shows "Unknown publisher".
- Development builds on Linux look for the helper next to the app. Only
  packaged builds enforce the root-owned path.
- If the disk is in use after a flash, FlashIt reports a warning and does
  not force the eject. The files are already written.
- Restarting to install an update during a flash stops the flash.

## Terms

- **authopen**: macOS tool that opens a file with elevated rights after
  asking the user.
- **Named pipe**: Windows channel between local processes, addressed by
  name.
- **polkit**: Linux service that authorizes administrative actions and
  shows the password prompt.
- **Socketpair**: two connected sockets created together. One is given to a
  child process, so no other process can reach it.
- **UAC**: User Account Control, the Windows prompt to allow an app to make
  changes.
- **Unix socket**: local channel between processes that can also carry open
  files.
