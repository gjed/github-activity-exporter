// Local semantic-release plugin: writes the generated release notes to
// /tmp/release-notes.md for `goreleaser release --release-notes`.
//
// Notes are taken from the plugin context, never passed through a shell,
// so commit messages containing $(), backticks, etc. cannot be executed.
const fs = require("fs");

module.exports = {
  prepare(_pluginConfig, { nextRelease, logger }) {
    fs.writeFileSync("/tmp/release-notes.md", nextRelease.notes);
    logger.log("Wrote release notes to /tmp/release-notes.md");
  },
};
