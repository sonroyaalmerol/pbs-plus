# PBS D2D Backup

(NOT READY FOR PRODUCTION) A Proxmox Backup Server (PBS) proxy that adds a "disk-to-disk" file backup functionality within PBS Web UI.

## Table of Contents
- [Introduction](#introduction)
- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
- [Contributing](#contributing)
- [License](#license)

## Introduction
PBS D2D Backup is a proof of concept project aimed at enhancing the Proxmox Backup Server (PBS) by adding a disk-to-disk file backup functionality directly within the PBS Web UI without the need of manually setting up cronjob on separate clients. It serves a modified version web UI at port `8008` (by default) with an added "Disk Backup" page. This project ultimately would aim to make Proxmox Backup Server a backup solution for bare-metal workstations with agents across multiple OSes including Windows.

## Features
- Disk-to-disk file backup functionality.
- Integration with Proxmox Backup Server Web UI.
- Easy to use and configure.
- Windows agent.

## Installation
To install PBS D2D Backup, follow these steps:
- (Currently building install scripts for different platforms.)

## Usage
There are mainly 2 parts in this project: server and agent. The server ideally should be installed within the PBS machine while agents are installed in the workstations.
### Server
- When the service is running, it will be hosting a reverse proxy of the PBS Web UI but with some JavaScript modifications to add the "Disk Backup" page and additional API paths for all the functionality within that page.
- All the additional features should be found in the "Disk Backup" page.
### Agent
- Currently, only Windows agents are available.
- As soon as the agent runs, it calls back to the server and registers itself while exchanging public keys for future SSH/SFTP connections.
- The Agent is just a service that communicates with the server via SSH/SFTP. When a backup is initiated server-side, the server opens up an SFTP connection to the agent and mounts the volume to PBS as a filesystem via rclone. proxmox-backup-client is then executed server-side to do the actual backup.

## Contributing
Contributions are welcome! Please fork the repository and create a pull request with your changes. Make sure to follow the existing code style and include tests for any new features or bug fixes.

## License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for more details.
