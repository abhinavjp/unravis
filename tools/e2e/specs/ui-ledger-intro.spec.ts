// BL-495g: the Ledger drawer says what it is once, and keeps the privacy notice; the three intro lines of the sweep (ANALYSIS finding,
// "Ledger drawer has three intro lines before the form") are two now.
import { test, expect } from '../support/fixtures';

test('LEDGERINTRO-01: the Ledger drawer has the plain line and the privacy notice before the list, no heading intro', async ({ page }) => {
  await page.getByRole('button', { name: 'Ledger', exact: true }).first().click();
  const drawer = page.locator('.ledger-drawer');
  await expect(drawer.getByRole('heading', { name: 'Ledger', exact: true })).toBeVisible();
  // The heading block holds the title and the close button only.
  await expect(drawer.locator('.drawer-heading p')).toHaveCount(0);
  await expect(drawer.getByText('What we believe, what supports it')).toHaveCount(0);
  // What stays: the one plain line (with its "In simple words" button) and the privacy notice.
  await expect(drawer.getByText('A list of theories to check, each with its evidence, kept in a local file.')).toBeVisible();
  await expect(drawer.getByRole('button', { name: 'Explain in simple words' })).toBeVisible();
  await expect(drawer.getByText('The ledger keeps theories and references only.')).toBeVisible();
  // Paragraphs above the "New theory" field: the plain line, the notice and the empty-list line. A fourth would be an intro again.
  const above = await drawer.locator('p').evaluateAll((nodes) => {
    const field = document.querySelector('#ledger-new');
    return nodes.filter((n) => (n as HTMLElement).offsetParent !== null && field && n.compareDocumentPosition(field) & Node.DOCUMENT_POSITION_FOLLOWING).length;
  });
  expect(above).toBe(3);
});
