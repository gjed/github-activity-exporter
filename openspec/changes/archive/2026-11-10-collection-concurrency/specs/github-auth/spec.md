# Spec Delta

## ADDED Requirements

### Requirement: Secondary rate limit pause

The system SHALL treat a 403 or 429 response as a secondary rate limit when it carries `Retry-After`, or when it is a 429 or its body names a secondary rate limit and `X-RateLimit-Remaining` is not 0. It SHALL then pause every request of the target, in every pool, until the time GitHub gives, and log a warning with the wait.

#### Scenario: Retry-After

- **WHEN** a request of a target gets a 403 with `Retry-After: 30`
- **THEN** no request of that target is sent for 30 seconds, whichever pool it counts against

#### Scenario: No wait given

- **WHEN** a request gets a 429 with neither `Retry-After` nor `X-RateLimit-Remaining: 0`
- **THEN** the target pauses for 60 seconds, doubling on each further consecutive secondary rate limit response of the same request

#### Scenario: Other targets unaffected

- **WHEN** one target is paused for a secondary rate limit
- **THEN** requests of other targets are sent without waiting

#### Scenario: Not a rate limit

- **WHEN** a request gets a 403 without `Retry-After`, with `X-RateLimit-Remaining` above 0, and a body that names no rate limit
- **THEN** the response is returned as is, with no pause and no retry

### Requirement: Rate limit retry

After a secondary rate limit pause, or a 403 or 429 with `X-RateLimit-Remaining: 0` once its pool's window resets, the system SHALL send the request again, at most 3 times. When the retries are spent the last response SHALL be returned to the caller.

#### Scenario: Retry succeeds

- **WHEN** a request gets a secondary rate limit response and the retry after the pause gets 200
- **THEN** the caller receives the 200 response and the repo is collected normally

#### Scenario: Retries spent

- **WHEN** a request gets a secondary rate limit response on the first attempt and on 3 retries
- **THEN** the caller receives the last 403 or 429 response, and the repo's collection fails as with any other API error

#### Scenario: Primary limit exhausted

- **WHEN** a request to the `graphql` pool gets a 403 with `X-RateLimit-Remaining: 0` and `X-RateLimit-Reset` in 10 minutes
- **THEN** only the `graphql` pool is paused until the reset, the request is sent again after it, and requests to `core` are sent without waiting

#### Scenario: Shutdown during a pause

- **WHEN** the request's context is cancelled during a pause
- **THEN** the request returns the context error at once

### Requirement: Rate limit and auth handling with concurrent requests

The primary pool pause, the secondary rate limit pause and the 401 reporting used by `/healthz` SHALL behave the same when many requests of a target run at once.

#### Scenario: Concurrent requests during a pool pause

- **WHEN** a pool of a target is paused and several workers send requests to it
- **THEN** every one of them waits for the reset, and the race detector reports no data race

#### Scenario: Concurrent requests during a secondary pause

- **WHEN** one worker's request gets a secondary rate limit response while other workers of the target are sending requests
- **THEN** every request of the target sent after that response waits for the same pause

#### Scenario: 401 from concurrent requests

- **WHEN** concurrent requests of a target are answered with 401
- **THEN** each response is reported once to the health tracker, as with serial requests
