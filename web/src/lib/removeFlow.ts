/**
 * Runs one removal and says whether the row is gone. A call that returns false or throws is a failure, so the row stays and the
 * person sees why. Nothing is thrown out of here: the caller only has to pick the sentence.
 */
export async function runRemoval(remove: () => Promise<boolean>): Promise<'removed' | 'failed'> {
  try {
    return (await remove()) === true ? 'removed' : 'failed';
  } catch {
    return 'failed';
  }
}
