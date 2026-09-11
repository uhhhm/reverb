# Recommendations are generated on each device

Every device builds its own recommendations from the synced taste inputs (plays,
library and playlist contents, and not-interested marks), instead of the server
generating them and sending results out. Desktop is the primary way Reverb runs,
and it must recommend while offline or unpaired. Because the inputs already sync,
devices converge without adding a new replicated entity.

## Consequences

Two devices can show slightly different Mixes when one has not synced recently.
Scheduled Mixes refresh at fixed local times using a seed shared per period, so
devices with the same inputs produce the same results. Recommendation output is
never written to the change log. Only the inputs sync.
