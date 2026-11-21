/**
 * Files handed to the Add files pop-up from another screen. The Add data > Files tile and its drop zone open the pop-up, which is the one
 * flow that adds files to a case that exists (it checks them with the server before anything is kept). A file dropped on the tile is
 * kept here for the pop-up to take when it opens, once.
 */
let pending: File[] = [];

export function setImportSeed(files: File[]): void { pending = files.slice(); }

/** The files waiting for the pop-up, and forgets them. */
export function takeImportSeed(): File[] {
  const files = pending;
  pending = [];
  return files;
}
