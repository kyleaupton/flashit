<p align="center">
  <img src="build/appicon.png" alt="FlashIt Logo" width="128" height="128">
</p>

<h1 align="center">FlashIt</h1>

<p align="center">
  <strong>Create bootable USB installers for any OS, from any OS.</strong>
</p>

<p align="center">
  <a href="https://github.com/kyleaupton/flashit/releases/latest">
    <img src="https://img.shields.io/badge/Download-Latest%20Release-blue?style=for-the-badge" alt="Download">
  </a>
</p>

---

FlashIt is a cross-platform desktop application that creates bootable USB drives for Windows, Linux, and other operating systems.

## Features

- **Windows USB Creation** - Create bootable Windows 10/11 installers with automatic WIM splitting for FAT32 compatibility
- **Linux USB Creation** - Create bootable Linux installers for Ubuntu, Fedora, Debian, and other distributions
- **Cross-Platform** - Native app for macOS, Windows, and ~~Linux~~ *(coming soon)*
- **Secure Privilege Elevation** - Uses a proper signed privileged helper on macOS and UAC on Windows for safe, system-native permission handling

## Platform Support

| Host OS | Windows USB | Linux USB |
|---------|-------------|-----------|
| macOS   | Yes         | Yes       |
| Windows | Yes         | Yes       |
| ~~Linux~~   | ~~Yes~~         | ~~Yes~~       |

*Linux host support coming soon*

## Screenshots

*Screenshots coming soon*

<!--
<p align="center">
  <img src="assets/screenshot-1.png" alt="FlashIt Screenshot" width="600">
</p>
-->

## Usage

1. **Choose your ISO** - Select the ISO file you want to flash
2. **Select target USB** - Pick the USB drive (only removable drives are shown)
3. **Flash** - Click start and wait for completion

## Safety

FlashIt includes multiple safety mechanisms:

- **Boot drive protection** - Refuses to write to system drives
- **Removable-only filtering** - Only shows removable USB drives as targets
- **Privilege verification** - Uses platform-native elevation (authopen on macOS, pkexec on Linux, UAC on Windows)

## Pure Go WIM Implementation

Creating Windows bootable USBs requires splitting large `install.wim` files to fit within FAT32's 4GB file size limit. Most tools rely on [wimlib](https://wimlib.net/), a C library that can be difficult to build and distribute across platforms.

FlashIt includes a **pure Go implementation** of WIM file reading and splitting. This means:

- **No external dependencies** - Everything is bundled in the app
- **No native libraries** - No need to install wimlib or any other tools
- **Truly cross-platform** - The same code runs on macOS, Windows, and Linux

## Roadmap

- [ ] **Linux Host Support** - Native Linux build of the application
- [ ] **Write Verification** - Verify USB contents after flashing to ensure a successful write
- [ ] **ISO Downloads** - Download popular Linux distributions directly from the app
- [ ] **Support More Install Options**
  - [ ] memtest86+
  - [ ] FreeDOS
  - [ ] macOS (will only work on macOS hosts)

## Acknowledgments

- WIM format documentation from [Microsoft](https://docs.microsoft.com/en-us/windows-hardware/manufacture/desktop/wim-vs-ffu-image-file-formats)
- WIM reader based on [Microsoft/go-winio](https://github.com/Microsoft/go-winio) (MIT License), modified for cross-platform support

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
