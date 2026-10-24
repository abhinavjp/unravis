/** Evidence labels: the only strength words the product may show (roadmap Conventions). */
export type EvidenceLabel = 'Symptom' | 'Correlation' | 'Strong correlation';

/** One plain-language term. Every technical word on screen has exactly one entry (roadmap §8.14). */
export interface Term {
  /** Stable kebab-case id, used for hint lookup and glossary anchors. */
  id: string;
  /** The word as shown on screen, for example "P95". */
  term: string;
  /** One-sentence plain definition shown in the small hint. */
  hint: string;
  /** Two-sentence glossary definition. */
  definition: string;
  /** "What it does not mean" line; required wherever a reader could over-read. */
  notMeaning: string;
  /** Other spellings that map to this term. */
  aliases?: string[];
}

/** The single reviewable copy deck. All on-screen words live here, not in components. */
export interface Deck {
  terms: Term[];
}
