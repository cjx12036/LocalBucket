# LocalBucket unified bucket view

Approved by the user in chat before implementation.

## Intent

Make transferring files between two computers feel like using one shared space. Remove the computer-selection screen. Keep native Mac and Windows apps, existing storage, discovery, permissions and copy semantics.

## Approach selected by the user

A unified view of files held by online computers. Files remain on their original computer and become unavailable when it goes offline. Full replication was considered and rejected for this iteration because it adds storage duplication and synchronization semantics the user did not request.

## Approved interaction details

- Launch into “共享空间”, showing root entries from all online peers. Retain each entry's stable owner ID and relative path throughout selection, navigation and transfer.
- Show a subdued “位置” field with the source computer name, so duplicate names are distinguishable. Do not merge or hide same-name files across computers.
- Each folder opens its owner's folder. Same-name folders on different computers remain separate entries; do not synthesize merged directories.
- Dropping/adding at the shared-space root stores files in this computer's dedicated bucket. Dropping within an open folder stores them in that folder on its owner. Brief destination text explains where the upload goes.
- Selected entries from multiple computers can be copied/downloaded together. Preserve originals and existing collision rules. Internal drag back into shared space does not create another copy. Local deletion only; disable deletion when a selection contains remote items.
- Refresh updates available entries without changing selection identities. A failed/offline peer does not suppress healthy peers' files. Report unavailable peers unobtrusively. Closing still exits and stops sharing.

## Layout

Use a restrained native layout on both platforms: compact title and online status; prominent Add and Save actions; breadcrumb and search; spacious dominant file list with name, size, location and modification details; compact transfer/status footer. Put storage location, refresh, diagnostics and instructions into a secondary More/Help entry instead of permanently occupying two tool rows. Keep keyboard access, multi-selection, folder navigation, drag affordances, task cancellation and error recovery.

## Documentation and release

Concise bilingual root README, with motivation first: “两台电脑传文件，还在来回折腾？LocalBucket，让文件拖过去就好。” / “Moving files between two computers shouldn’t be a chore. Just drop them into LocalBucket.” Explain download, unified-space behavior, local-only deletion and no replication. Explicitly state unsigned Windows may trigger SmartScreen; Mac is ad-hoc signed and unnotarized, may require Open Anyway. Do not promise bypassing OS security. Update app instructions and release a new GitHub preview with checksums and evidence.

## Validation

Shared aggregation, duplicate ownership, independent peer failures, mixed-owner transfer, upload destinations and selection stability need verification. Compile both native UIs, inspect Mac layout and packaged startup, run existing meaningful core regressions and Windows CI launch smoke test. Actual Win10 drag/drop remains a user device check; signing/certificates remain outside this iteration.
