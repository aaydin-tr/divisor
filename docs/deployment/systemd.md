# Running divisor with systemd

On a traditional Linux host, run divisor as a systemd service:
[`examples/systemd/divisor.service`](../../examples/systemd/divisor.service) is
the unit and [`examples/systemd/config.yaml`](../../examples/systemd/config.yaml)
a config to start from. systemd starts divisor at boot, restarts it if it
fails, sends `SIGHUP` on `systemctl reload`, and stores both of divisor's log
streams in the journal.

## Install

1. Install the binary as `/usr/local/bin/divisor`, from a
   [release archive](../getting-started.md#1-install-divisor) or a build of
   your own, and check it with `divisor --version`.
2. Create the system user the unit runs as:

   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin divisor
   ```

3. Install the config. Edit its `backends` first; the example lists two
   placeholder addresses.

   ```bash
   sudo install -d -m 0755 /etc/divisor
   sudo install -m 0644 config.yaml /etc/divisor/config.yaml
   ```

   The `divisor` user must be able to read the file and enter the directory,
   or divisor exits at startup with `permission denied`. A TLS key
   (`server.key_file`) need not be world-readable: `chown root:divisor` and
   `chmod 0640` it.

4. Install and start the unit:

   ```bash
   sudo install -m 0644 divisor.service /etc/systemd/system/divisor.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now divisor
   systemctl status divisor
   ```

Divisor Probes every Backend once before it starts listening, so the ports open
a few seconds after the start when some Backends do not answer. A Backend that
is Down at startup Rejoins as soon as a Probe succeeds; until one is Alive,
divisor answers `503`.

## The unit

- `ExecStart` passes `--config` with an absolute path. Without the flag
  divisor reads `./config.yaml` from its working directory, which is `/` under
  systemd.
- `Restart=on-failure` restarts divisor 5 seconds after any non-zero exit. A
  config that fails validation exits non-zero too, so a broken config shows up
  as a restart loop: `systemctl status divisor` and the journal name the
  error.
- `TimeoutStopSec=40s`: on `systemctl stop`, divisor stops accepting
  connections and drains in-flight requests for up to
  `server.graceful_shutdown_timeout` (default `30s`). If you raise that, raise
  `TimeoutStopSec` with it, or systemd kills divisor mid-drain.
- `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp` and
  `PrivateDevices` sandbox the process; divisor writes no files, so the
  read-only file system costs nothing.
- Divisor runs as an unprivileged user, so it cannot bind a port below 1024.
  For `port: 80` or `443`, uncomment `AmbientCapabilities=CAP_NET_BIND_SERVICE`.

Change the unit with a drop-in rather than editing the installed file:
`sudo systemctl edit divisor`.

## Reload

A Reload applies an edited config without a restart: only `backends`,
`health_checker_time` and `health_checker_timeout` may change. Divisor watches
its config file and Reloads about a second after an edit, so saving
`/etc/divisor/config.yaml` is enough. `systemctl reload divisor` sends
`SIGHUP`, which Reloads too.

An edit that touches any other key, or that does not validate, is rejected as a
whole: divisor keeps serving the running config and logs which keys need a
restart. Apply those with `sudo systemctl restart divisor`. The rules are in
[Configuration](../configuration.md#reload).

`systemctl reload` returns before the Reload finishes and reports success
either way; the outcome is in the journal:

```bash
journalctl -u divisor | grep Reload
```

Each Reload logs `Reload applied` with the added and removed Backends, or
`Reload rejected, divisor keeps serving the running config` with the reason.

## Logs

Divisor writes Application logs to stderr and, with
`logging.access_log: true`, the Access log to stdout. Under this unit the
journal stores both:

```bash
journalctl -u divisor -f
```

The journal does not record which stream a line came from, but the lines tell
themselves apart: Access log lines carry `client_ip` and no `level`.

```bash
journalctl -u divisor -o cat | grep '"client_ip"'      # the Access log
journalctl -u divisor -o cat | grep -v '"client_ip"'   # Application logs
```

To keep the Access log out of the journal, send stdout to a file of its own
with a drop-in (`sudo systemctl edit divisor`):

```ini
[Service]
StandardOutput=append:/var/log/divisor/access.log
StandardError=journal
```

Create `/var/log/divisor` before restarting (`sudo install -d -m 0755
/var/log/divisor`); systemd opens the file as root before divisor starts and
fails the start if the directory is missing. Rotate the file with logrotate's
`copytruncate`: divisor never reopens it.

What each log line holds is in [Observability](../observability.md).
