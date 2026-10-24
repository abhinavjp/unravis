/**
 * Pure lookups over the copy deck, used by the Hint and Glossary components (P14a). Nothing here touches the DOM,
 * the network or storage.
 */
import { deck } from './deck';
import type { Term } from './types';

const byId = new Map<string, Term>(deck.terms.map(term => [term.id, term]));

/** The term with this id, or undefined when the deck has none. */
export function findTerm(id: string | null | undefined): Term | undefined {
  return id ? byId.get(id) : undefined;
}

/** Alphabetical by the word as shown, ignoring case. Returns a new array. */
export function sortTerms(terms: readonly Term[]): Term[] {
  return [...terms].sort((a, b) => a.term.localeCompare(b.term, 'en', { sensitivity: 'base', numeric: true }));
}

/** Every deck term, alphabetical. */
export function allTermsSorted(): Term[] {
  return sortTerms(deck.terms);
}

/**
 * Terms matching a text query, alphabetical. Case-insensitive; every word of the query must appear somewhere in the
 * term, its aliases, hint, definition or "what it does not mean" line. An empty query returns every term.
 */
export function searchTerms(query: string, terms: readonly Term[] = deck.terms): Term[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return sortTerms(terms);
  const matches = terms.filter(term => {
    const haystack = [term.term, ...(term.aliases ?? []), term.hint, term.definition, term.notMeaning].join(' ').toLowerCase();
    return words.every(word => haystack.includes(word));
  });
  return sortTerms(matches);
}
