## MODIFIED Requirements

### Requirement: Issues only

Issue collection SHALL request issues only. It SHALL NOT fetch pull requests, so its cost per repo SHALL NOT grow with the repo's pull request history.

#### Scenario: Repo with many PRs and few issues

- **WHEN** a repo has thousands of pull requests and a handful of issues, and no title pattern is configured
- **THEN** the repo is read within its batch's request, plus one request per further 100 repo labels, and no pull request is returned by any request
