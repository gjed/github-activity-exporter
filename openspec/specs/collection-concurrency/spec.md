# collection-concurrency Specification

## Purpose

Collect a target's GitHub data with a bounded number of concurrent requests, so a cycle on a large org fits its collection interval without changing the exported series.

## Requirements

### Requirement: Concurrency setting

The system SHALL read `collection.concurrency` at the top level of the config file as the default for every target, and `collection.concurrency` on a target as that target's override. The value SHALL be an integer from 1 to 100; the default SHALL be 4.

#### Scenario: Default

- **WHEN** neither the top level nor the target sets `collection.concurrency`
- **THEN** the target collects with concurrency 4

#### Scenario: Global default

- **WHEN** the top level sets `collection.concurrency: 8` and the target sets none
- **THEN** the target collects with concurrency 8

#### Scenario: Target override

- **WHEN** the top level sets `collection.concurrency: 8` and the target sets `collection.concurrency: 2`
- **THEN** the target collects with concurrency 2

#### Scenario: Out of range

- **WHEN** the top level or a target sets `collection.concurrency` to 0 or 101
- **THEN** startup fails with an error naming the setting, and the target when it is a target setting

### Requirement: Bounded concurrent requests per target

The system SHALL run at most `collection.concurrency` units of collection work of a target at once, across all its collectors. A unit SHALL send its GitHub requests one after another. A unit is one repo of the PR collector, one batch of the issue or Dependency Dashboard collector including its per-repo fallback queries, or one repo of the issue listing used with title filters.

#### Scenario: Limit across collectors

- **WHEN** a target with concurrency 4 collects PRs, issues and the Dependency Dashboard in one cycle
- **THEN** no more than 4 collection requests of that target are in flight at any time

#### Scenario: Serial at concurrency 1

- **WHEN** a target has concurrency 1
- **THEN** its collection requests are sent one at a time

#### Scenario: Targets are independent

- **WHEN** two targets each have concurrency 4
- **THEN** each target has up to 4 collection requests in flight, independently of the other

### Requirement: Collectors run concurrently

The system SHALL run the PR, issue and Dependency Dashboard collectors of a cycle at the same time, sharing the target's concurrency limit, and SHALL finish the cycle when all three have finished.

#### Scenario: Cycle end

- **WHEN** the PR collector is still running and the issue and dashboard collectors have finished
- **THEN** the cycle's snapshot update, scrape duration and flush happen only after the PR collector finishes

### Requirement: Identical output for any concurrency

The exported series and values of a cycle SHALL NOT depend on `collection.concurrency`: the same repos and GitHub responses SHALL produce the same open-state gauges, event counters, failed repos and `github_exporter.api.errors` increments at any concurrency.

#### Scenario: Serial and concurrent cycles agree

- **WHEN** the same sequence of cycles runs on the same fixtures with concurrency 1 and with concurrency 8
- **THEN** both export the same series with the same values

#### Scenario: Event window under concurrency

- **WHEN** PR events are scanned with concurrency greater than 1
- **THEN** each event is counted once across cycles, and a repo whose collection fails keeps its previous event window, as with concurrency 1

### Requirement: Cancellation stops new work

When the collection loop is stopped, the system SHALL start no new unit of work and SHALL return once the units in flight return.

#### Scenario: Shutdown mid-cycle

- **WHEN** the loop's context is cancelled while units are queued
- **THEN** the queued units are not started and the cycle returns without waiting for them
