/**
 * Reads what was dropped on the "Import files" panel, folders included (packet "folder handling"). A folder drop gives a directory entry, not
 * files, so the folder is walked with the file system entry API that browsers offer to a drop. Pure of the DOM except for the objects it is
 * handed, so a test passes plain fakes with the same shape. Nothing is read from a file here, only its name and size.
 */

/** The part of a FileSystemEntry this module uses. */
export interface DropEntry {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  fullPath?: string;
  file?(success: (file: File) => void, failure?: (error: unknown) => void): void;
  createReader?(): DropReader;
}

export interface DropReader {
  /** Gives the next batch of entries, and an empty batch at the end. */
  readEntries(success: (entries: DropEntry[]) => void, failure?: (error: unknown) => void): void;
}

/** The part of a DataTransferItem this module uses. */
export interface DropItem {
  kind: string;
  webkitGetAsEntry?(): DropEntry | null;
  getAsFile?(): File | null;
}

export interface DropResult {
  files: File[];
  /** At least one dropped item was a folder. */
  folder: boolean;
  /** The walk stopped at a limit (too many files, or folders too deep); something in the drop was not listed. */
  truncated: boolean;
  /** The walk stopped at the file limit: more files were in the folders. */
  tooMany: boolean;
  /** The walk stopped at the depth limit: folders deeper than that were not opened. */
  tooDeep: boolean;
  /** Entries that could not be read (a folder or file the browser refused). */
  unreadable: number;
}

/** Most files one drop may bring. A folder of logs is hundreds of files; a whole disk is not wanted. */
export const MAX_DROPPED_FILES = 1000;
/** How deep the walk goes into folders inside folders. */
export const MAX_DROP_DEPTH = 12;

/** Gives the file the path it came from, as the folder picker does, so the list shows `logs/W3SVC1/u_ex261020.log`. */
function withPath(file: File, path: string): File {
  const clean = path.replace(/^\/+/, '');
  if (clean === '' || clean === file.name) return file;
  try { Object.defineProperty(file, 'webkitRelativePath', { value: clean, configurable: true }); } catch { /* the plain name is used */ }
  return file;
}

const readFile = (entry: DropEntry): Promise<File | null> =>
  new Promise((resolve) => {
    if (!entry.file) { resolve(null); return; }
    try { entry.file((file) => resolve(file), () => resolve(null)); } catch { resolve(null); }
  });

const readBatch = (reader: DropReader): Promise<DropEntry[] | null> =>
  new Promise((resolve) => {
    try { reader.readEntries((entries) => resolve(entries), () => resolve(null)); } catch { resolve(null); }
  });

/** Walks one entry and appends the files under it, in the order the browser lists them. */
async function walk(entry: DropEntry, path: string, depth: number, out: DropResult): Promise<void> {
  if (out.files.length >= MAX_DROPPED_FILES) { out.truncated = true; out.tooMany = true; return; }
  if (entry.isFile) {
    const file = await readFile(entry);
    if (!file) { out.unreadable++; return; }
    out.files.push(withPath(file, path));
    return;
  }
  if (!entry.isDirectory || !entry.createReader) return;
  out.folder = true;
  if (depth >= MAX_DROP_DEPTH) { out.truncated = true; out.tooDeep = true; return; }
  const reader = entry.createReader();
  // A reader gives its entries in batches (100 at a time in Chrome); an empty batch is the end.
  for (;;) {
    const batch = await readBatch(reader);
    if (batch === null) { out.unreadable++; return; }
    if (batch.length === 0) return;
    for (const child of batch) {
      if (out.files.length >= MAX_DROPPED_FILES) { out.truncated = true; out.tooMany = true; return; }
      await walk(child, path + '/' + child.name, depth + 1, out);
    }
  }
}

/**
 * The files of a drop. `items` are the drop's data transfer items and `plain` its plain file list (used when the browser has no entry API,
 * and then a folder arrives as nothing readable, which is told apart by `folder` staying false and the list being empty).
 * The entries are taken first, before anything is awaited: a browser clears the drop's items as soon as the event ends.
 */
export async function filesFromDrop(items: readonly DropItem[] | null | undefined, plain: ArrayLike<File> | null | undefined): Promise<DropResult> {
  const out: DropResult = { files: [], folder: false, truncated: false, tooMany: false, tooDeep: false, unreadable: 0 };
  const entries: DropEntry[] = [];
  const loose: File[] = [];
  let haveEntryApi = false;
  for (const item of Array.from(items ?? [])) {
    if (item.kind !== 'file') continue;
    const entry = typeof item.webkitGetAsEntry === 'function' ? item.webkitGetAsEntry() : null;
    if (entry) { haveEntryApi = true; entries.push(entry); continue; }
    const file = typeof item.getAsFile === 'function' ? item.getAsFile() : null;
    if (file) loose.push(file);
  }
  if (!haveEntryApi && entries.length === 0) {
    out.files.push(...loose.length > 0 ? loose : Array.from(plain ?? []));
    return out;
  }
  for (const entry of entries) await walk(entry, entry.name, 0, out);
  out.files.push(...loose);
  return out;
}
