# S3 WebDAV Sync Design

Date: 2026-06-05

## Goal

Allow users to copy files or folders from the active S3 WebDAV mount to one or more other configured mounts from the file browser.

## Decisions

- Sync is copy/update only. It never deletes extra files from the target mount.
- The source can be the current folder, a selected folder, or a selected file.
- Folder sync is recursive by default, including root path `/`.
- Root path `/` means the visible root of that mount. If the mount has `root_prefix`, only objects under that prefix are synced.
- Target path defaults to the same path as the source and can be edited before running.
- Existing target files are skipped by default.
- Users may choose overwrite, but the UI must show a second confirmation because overwrite is destructive.
- The source mount cannot be selected as a target.
- Disabled or incomplete mounts cannot be selected as targets.

## API

Add:

- `POST /api/s3/files/sync`
- `GET /api/s3/files/sync/{job_id}`

Request:

- `source_mount_key`
- `target_mount_keys`
- `source_path`
- `destination_path`
- `overwrite`

Start response and status response:

- `job_id`
- `status`: `running`, `completed`, or `failed`
- source mount/path and destination path
- current target mount, current source path, and current destination path
- total file count when known
- processed file count
- total bytes, copied bytes, current file size, and copied bytes for the current file when known
- aggregate copied/skipped/failed counts
- per-target copied/skipped/failed counts
- per-target error messages

## UX

- Add a Sync action in the file toolbar for the current folder.
- Add a Sync row action for each file or folder.
- Open a two-pane modal instead of a plain target checklist.
- The left pane is the source bucket tree. It defaults to the active mount and the current/row path.
- The right pane is the target bucket tree. It defaults to another enabled and ready mount.
- Direction is fixed and visible: left source to right target. The UI does not imply bidirectional sync.
- Users choose the target directory from the right tree.
- If the source is a file, the final target path is `target directory + file name`.
- If the source is a non-root folder, the final target path is `target directory + source folder name`.
- If the source is `/`, the final target path is the selected target directory and all visible source children are copied under it.
- Show a plain warning when overwrite is selected.
- While syncing, show progress, current object, target mount, and copied/skipped/failed counts.
- After sync, show a result summary in the modal and toast a short aggregate result.

## Verification

- Service tests cover single-file sync, skip conflicts, overwrite conflicts, recursive folder sync, root sync, and invalid target validation.
- Server tests cover the new API route.
- Frontend syntax check covers the S3 JavaScript changes.
