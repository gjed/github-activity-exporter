# Spec Delta

## ADDED Requirements

### Requirement: Per-target API base URL

The system SHALL let each target set `base_url`, the GitHub REST API root, defaulting to `https://api.github.com`. Every outbound GitHub call for that target, including REST, GraphQL, GitHub App token exchange and the startup credential check, SHALL go to the host and path derived from that target's `base_url`.

#### Scenario: Default targets github.com

- **WHEN** a target does not set `base_url`
- **THEN** REST calls go to `https://api.github.com` and GraphQL calls go to `https://api.github.com/graphql`

#### Scenario: GitHub Enterprise Server

- **WHEN** a target sets `base_url: https://ghes.example.com/api/v3`
- **THEN** REST calls, the GitHub App installation token exchange and the startup credential check go to `https://ghes.example.com/api/v3`, GraphQL calls go to `https://ghes.example.com/api/graphql`, and no call for that target goes to `api.github.com`

#### Scenario: Two targets with different base URLs stay isolated

- **WHEN** target A uses one `base_url` and target B uses another
- **THEN** no API call for target A goes to target B's host, and vice versa

#### Scenario: Invalid base URL is fatal

- **WHEN** a target's `base_url` is not an `http` or `https` URL with a host
- **THEN** the exporter exits at startup with an error naming the target and the `base_url` value
