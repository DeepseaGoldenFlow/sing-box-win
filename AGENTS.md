# SFW — sing-box for Windows

## Product target

SFW is a Windows native client whose product behavior is aligned with the user's installed SFM (sing-box for Mac). Match configuration semantics, routing, groups, connections, logs, endpoints, lifecycle and user workflows. Use Windows equivalents for Apple-specific system facilities. Preserve the project's unofficial-client notice and GPL-3.0 license.

The SFM feature inventory is not a list of completed SFW features. Current differences and evidence are tracked in docs/sfw-sfm-parity-audit.md. Do not claim full parity while required capabilities such as TUN remain missing.

## Acceptance rules

- A visible control is complete only when it calls the real backend and its intended outcome has been verified. No inert controls presented as implemented features.
- Distinguish configured, cached and live state. Missing live data must not be labelled authenticated, online or connected.
- Failed saves, updates and profile switches must preserve the previous usable configuration. Preserve the user's system proxy preference across internal restarts and restore prior OS proxy values when stopping.
- Preserve source configuration semantics, including DNS, routes, endpoints and configured controller modes. Document required platform overrides.
- Only show configured groups and modes supported by the live profile. Synthetic compatibility API objects must not masquerade as subscription groups.
- Verify TUN and system proxy separately. HTTP connectivity checks do not prove full-system routing, DNS, UDP or Tailscale reachability.
- Report the scope of validation. Builds and API tests do not substitute for native GUI, reboot, sleep/wake and network-change acceptance.
- Keep project code in this repository under the user's IdeaProjects root. Keep intermediate files outside the source tree; do not commit subscriptions, authentication data, runtime databases or build output.

## Validation

Use the existing Makefile's toolchain and build tags. Run the relevant Go tests and Linux/Windows vet for backend changes. Build the native client with the installed Visual Studio MSBuild and self-contained win-x64 settings as implemented by scripts/build-native.ps1. Test network-changing controls with isolated fixtures when they would disrupt the user's active traffic.
