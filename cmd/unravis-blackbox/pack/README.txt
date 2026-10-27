Unravis Blackbox
================

The Blackbox records CPU, memory, disk, network and the busiest processes of
this server every 15 seconds and keeps the last 7 days. When something goes
wrong, the evidence is already there. It only reads this server. It does not
record request bodies, passwords or the content of log files.

Install (run PowerShell as administrator, in this folder)
---------------------------------------------------------
    .\unravis-blackbox.exe install

It names the Blackbox after this computer (add --name web-b to choose), makes
the recordings folder (D:\UnravisBlackbox, or C:\UnravisBlackbox when there is
no D: drive), installs the UnravisBlackbox service and starts it.

The READ KEY is printed once. Copy it now. Only a fingerprint of it is kept on
this server. Unravis asks for it when you add this Blackbox to a case.

If the install cannot finish it says why and removes what it started. The
reasons: no administrator rights, a Blackbox is already installed, port 7788 is
busy, the folder is read-only or has under 1 GB free, or antivirus blocked it.

Other commands
--------------
    unravis-blackbox stop        stop recording (the recordings are kept)
    unravis-blackbox start       start again
    unravis-blackbox apply       read blackbox.json again and restart
    unravis-blackbox snapshot    copy the recordings to <folder>\snapshots
    unravis-blackbox remove      remove the service; the recordings stay
    unravis-blackbox remove --delete-recordings --confirm-name <name>
                                 remove the service and delete the recordings
    unravis-blackbox run         record in this window (Ctrl+C stops it)
    unravis-blackbox version

Settings are in blackbox.json next to the program. After a change run
"unravis-blackbox apply". Housekeeping cannot be turned off: the oldest files
go first when the recordings pass keepDays, maxGB, or leave less than
minFreePercent of the drive free.

Status page
-----------
http://<this server>:7788/status answers with the read key in the
X-Blackbox-Key header (never in the address). /manifest, /files and
/files/<name> are read only too. Without a firewall rule only this computer
can reach it; allow port 7788 in Windows Firewall to let Unravis read it from
another PC.
