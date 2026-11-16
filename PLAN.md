# Project: DroidSleuth — Android Runtime Investigator

You are acting as a senior Go systems engineer, Android platform specialist, and open-source software architect.

I want to build DroidSleuth: an original, Linux-first, local-first, open-source Android runtime investigation tool written in Go.

This is a serious solo engineering project intended to become a useful developer tool, not a portfolio clone or a basic terminal dashboard.

**Your task is to investigate, challenge the project direction, and produce a rigorous implementation plan. Do not implement anything yet.**

## 1. Project vision

DroidSleuth connects to Android devices through ADB and helps developers understand unexpected application behavior.

The main question it should answer:

"My Android app restarted, crashed, froze, or experienced an unexplained performance spike. What happened before, during, and after that event?"

Unlike a traditional system monitor that displays raw CPU, RAM, and process statistics, DroidSleuth should construct a unified timeline of observable Android runtime events and provide evidence-backed explanations.

Example future workflow:

```bash
droidsleuth devices
droidsleuth watch com.example.app
droidsleuth investigate --last
droidsleuth replay session.db
```

These commands are illustrative, not finalized requirements.

## 2. Reference repository — mandatory investigation

A copy of adbtop is available at:

`../adbtop`

Its upstream repository is:

https://github.com/aabolfazl/adbtop

Inspect the repository thoroughly before proposing our architecture.

Investigate:
- Existing features and CLI workflows
- Process and application lifecycle monitoring
- Crash and ANR handling
- Session recording and report structure
- Event timestamps and historical data
- How Android metrics are collected over ADB
- Persistent shell sessions and collection efficiency
- Device capability detection
- Unsupported Android versions and permissions
- Existing event correlation or diagnostic capabilities
- Whether adbtop already solves our proposed use cases

Do not assume that a feature is missing simply because its README does not mention it.

Distinguish between:
1. Features directly confirmed in adbtop's code
2. Features documented but not verified in code
3. Features for which no implementation was found
4. Features whose status remains uncertain

Evaluate relevant competing tools, including Android Studio Profiler, Perfetto, Holo, and standard ADB debugging commands.

Produce a feature-gap matrix with evidence and exact source references wherever possible.

If DroidSleuth's proposed functionality is substantially already solved, explicitly say so and suggest a narrower or more valuable problem.

**Do not copy or port adbtop code. Do not modify the reference repository.**

## 3. Core proposed capabilities

### A. Process lifecycle investigation

Investigate the feasibility of detecting:

- Process appearance and disappearance
- PID changes and process restarts
- Application crashes and fatal exceptions
- ANRs
- Low-memory kills where observable
- Force stops and user-initiated termination where distinguishable
- Foreground/background transitions
- System-triggered process changes

Identify which events can actually be observed through ADB on unrooted devices.

Distinguish direct evidence from inference.

Never claim to know the reason a process died merely because its PID disappeared.

### B. Unified event timeline

Build a model that combines timestamped observations from:

- ADB logcat
- Process state snapshots
- CPU and memory measurements
- Android system services
- ActivityManager events, where available
- Optional Perfetto traces

Determine how to handle:

- Different clock domains
- Events arriving out of order
- Collection latency
- Missing timestamps
- Duplicate events
- ADB disconnections
- Process identity and PID reuse
- Lost observations
- Concurrent collectors

The event timeline should be useful even without a graphical interface.

### C. Evidence-backed investigation

Given an incident, the tool should show the relevant events surrounding it.

Example:

```text
Incident: Application process terminated

14:20:11.200  Memory usage increased
14:20:12.105  GC event observed
14:20:12.860  Fatal exception reported
14:20:12.904  Process disappeared
14:20:13.410  New process appeared

Finding:
Fatal exception was observed shortly before process
termination.

Evidence:
- Relevant logcat entry
- Process PID transition
- Associated timestamps
```

Do not build an ungrounded AI diagnosis feature.

Prefer deterministic rules with explicit evidence and confidence levels.

Separate:
- Observed facts
- Correlated events
- Possible explanations
- Unknown or unavailable information

### D. Session capture and replay

Support recording a reproducible diagnostic session.

Requirements:
- Local storage
- Versioned event schema
- Session metadata
- Offline replay
- No connected device required for analysis
- Bounded resource consumption
- Privacy-conscious log handling

Evaluate SQLite versus JSONL and justify the selected design.

### E. Terminal-first interface

The eventual UI should be inspired by the usability of btop++, but should focus on investigating Android runtime behavior.

Possible panels:
- Device overview
- Active processes
- Runtime event timeline
- Incident details
- Evidence inspector

Do not prioritize elaborate charts over working investigation features.

## 4. Technology constraints

Primary implementation language: Go.

Platform: Linux-first.

Potential libraries:
- Cobra for CLI
- Bubble Tea for TUI
- Lip Gloss for styling
- SQLite for storage

These are preferences, not mandatory decisions. Evaluate them.

The architecture should separate:

1. ADB transport
2. Android capability detection
3. Data collectors
4. Event normalization
5. Correlation engine
6. Session persistence
7. CLI and TUI presentation

Prefer clear interfaces and minimal dependencies.

Avoid premature plugin systems, microservices, unnecessary abstractions, or a large framework-like architecture.

No cloud services, authentication, telemetry, or hosted backend.

Target an unrooted Android device initially.

No custom Android companion APK for the MVP unless research demonstrates that it is indispensable.

## 5. Technical feasibility research

Before producing the final plan, determine:

- What data standard ADB exposes without root
- Which commands are usable on modern Android
- Differences between Android versions and OEM implementations
- Whether logcat exposes sufficient lifecycle information
- How reliable process restart detection can be
- How to associate logs with packages, UIDs, and process instances
- How collector overhead affects device performance
- Which failure types can be distinguished confidently
- Which claims are impossible without privileged access

Prototype-level command exploration is acceptable, but do not create project implementation files yet.

Never treat unsupported behavior as available.

## 6. Scope

Assume:
- One developer
- Approximately 4–8 weeks
- Practical Go experience
- Linux development environment
- Access to at least one physical Android device
- A high-quality open-source repository as the final deliverable

Keep the first milestone extremely focused.

The initial usable version should preferably answer:

"What happened around the time my Android app died or restarted?"

Defer complex frame profiling, network packet interception, GPU monitoring, machine learning, and multi-device orchestration unless essential.

## 7. Required outputs

Produce the following before any coding:

### A. Competitive analysis
Feature-by-feature comparison against adbtop and relevant alternatives, with references.

### B. Differentiation assessment
A candid assessment of whether the project offers a meaningful improvement for developers.

### C. Android feasibility matrix
For every proposed event, document its data source, required permissions, reliability, Android-version limitations, and fallback.

### D. MVP specification
Precisely define what v0.1 does and does not do.

### E. Architecture
Component diagram, data flow, package boundaries, concurrency model, and timestamp strategy.

### F. Repository structure
A minimal idiomatic Go project layout. Avoid excessive packages.

### G. Data model
Event schema, process identity, device identity, session format, incident representation, and evidence references.

### H. Milestone roadmap
A prioritized 4–8 week plan with dependencies, acceptance criteria, and testable deliverables.

### I. Validation strategy
Unit tests, fake ADB fixtures, replay tests, integration tests, and validation against real Android devices.

Include realistic testing for crashes, ANRs, force stops, and process restarts.

### J. Risks and alternatives
The five biggest technical or product risks, ways to validate them early, and clear fallback decisions.

### K. First implementation slice
Identify the smallest end-to-end feature we can implement first to validate the architecture.

## 8. Engineering quality

Prioritize:
- Correctness
- Observable behavior
- Explicit error handling
- Context cancellation
- Safe subprocess management
- Testability
- Performance
- Clear documentation
- Low device overhead

Account for device disconnection, collector failures, command timeouts, and interrupted sessions.

Avoid building elaborate abstractions before demonstrating that the underlying ADB observations work.

## 9. Planning rules

You must challenge assumptions.

Do not automatically agree with every proposed feature.

Do not propose functionality solely to make the project look impressive.

Do not use Go merely to recreate an existing Rust project.

If existing tools already handle a particular workflow well, exclude it unless DroidSleuth has a demonstrable improvement.

Do not write production code in this phase.

Produce the plan in a format suitable for later implementation, identify unresolved technical questions, and make a clear recommendation about whether to proceed.

**Start by auditing adbtop and establishing the real gaps. Then design DroidSleuth around a problem that remains worth solving.**
