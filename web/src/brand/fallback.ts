/**
 * The light palette as plain values, for charts that are built where there is no document (tests, server-side checks).
 * It is the one place outside tokens.css where colours are written, and tests/tokens.test.cjs checks every value against the light theme there.
 */
export const lightFallback = {
  text: '#16191d', accent: '#0b6b57', accentSoft: '#0b6b576b', warn: '#96600a', bad: '#b02a3d', pending: '#5b6878', text2: '#4a525c', axisLine: '#d9d9d4', grid: '#e8e8e4',
  empty: '#efefec', heat: ['#dce9f6', '#83bdb5', '#e4ae5b', '#b85a42'], surface: '#ffffff',
  series: ['#2457c5', '#9956a1', '#ad6511', '#b02a3d', '#6e6f24', '#157d94', '#76563f', '#5b6878']
};
