// Package secretscrub is the one place that finds the value of a secret setting in free text and replaces it with a mask. Three
// scrubbers (the case report and findings export in internal/evidence, the app log in internal/applog, the setup log in
// internal/setup) and the Releases app log reader (internal/releasedash) call it, so a new text form is fixed once.
//
// Mask covers these forms, in any text a person or a driver can put in a message:
//
//   - KEY=value and KEY: value, where the key is a password word (password, passwd, passphrase, pwd, "_pass", "_pin") or a
//     token word (secret, token, api key, access key, account key, shared access key, private key, credential), alone or inside a
//     longer name (DB_PASSWORD, MYSQL_PWD, client_secret, AWS_SECRET_ACCESS_KEY), also after a "?" or "&" in a query string;
//   - the JSON and Python forms ("password":"a\"b c", 'password': 'it\'s', and JSON written inside a JSON string, \"password\":\"x\");
//   - ADO, ODBC and JDBC values with spaces, ";" and doubled quotes, braced values ({a;b}}c}) and values on the next line;
//   - XML: <Password>x</Password>, <ns:ApiKey><![CDATA[x]]></ApiKey>, <add key="password" value="x"/>, password="x" attributes,
//     <setting name="Password"><value>x</value></setting>, and a connectionString attribute that holds a secret;
//   - user:password@ in a URL, also when the password holds an "@" or a space (MaskURLUserinfo).
//
// Rules the code keeps:
//
//   - Fail closed. When the end of a value is unclear (an unclosed quote, a brace that never ends, a tag without a close) the value
//     runs to the end of the text, so nothing after it is kept; a value that may hold spaces runs to the next ";" or line end. A tag
//     longer than maxTag counts as closed where it is cut: the attributes read so far are kept and the rest of it, up to the next "<",
//     is masked. (A text that ends inside a tag is not a tag.)
//   - A key with nothing after it ("password:" then a line at the same depth, "client_id: abc") does not take the next key as its value:
//     in YAML the value on the next line is indented deeper than the key (Mask and Carry).
//   - Ordinary text stays readable. A key is a name followed by "=" or ":", so the sentence "the password was reset" is not touched,
//     and names that only contain the word as a prefix of another word (tokenizer, secretary) or end in a size or a policy word
//     (token_count, password_min_length, max_tokens) are left alone.
//   - Not covered (out of scope, listed so nobody assumes it): entity-escaped markup (&lt;Password&gt;), \u-escaped key names,
//     zero-width or full-width letters in a key, URL-encoded "password%3Dx", headers such as Ocp-Apim-Subscription-Key and a SAS "sig="
//     parameter, "--password x" flags (the Releases reader has its own pattern), and text nodes after the first in <Password>a<b/>c</Password>.
//   - Not covered: a secret whose name and value are two separate fields of one record (BACKLOG SWP-1122-13): the Kubernetes and Helm
//     list "- name: DB_PASSWORD" then "  value: x", JSON {"name":"DB_PASSWORD","value":"x"}, and the plist <key>Password</key><string>x</string>.
//     Reading them needs the record around the key (the sibling line, the sibling JSON member, the sibling element), which is a second
//     reader with its own budget and its own Carry state, so it is not part of this change.
//   - Readers that see one line at a time (the Releases log reader) use Carry, which hides a YAML block scalar under "password: |" and a
//     value on the line after a key with nothing after it. They do not cover a quoted value or a JSON array that runs over several
//     lines, or a tag split over lines.
//   - A work budget (workFactor steps per byte plus a floor) bounds every call whatever the input: a text that costs more is not scanned
//     further and the rest of it is masked.
//   - Linear time. No regular expression here backtracks and every look-ahead is bounded or consumes what it reads, so one hostile
//     megabyte cannot cost more than a few hundred milliseconds.
//
// The key stays in the text and the value (or, for a quoted value, the text inside the quotes) becomes the mask; the callers keep
// their own older patterns and run them after Mask.
//
// Scan audit. Every loop and every strings.Index call in the package is either charged to the work budget ("charged"), bounded by a
// constant that does not depend on the input ("constant"), or moves the position past what it read so that no later step reads it
// again ("consumed"):
//
//	findWords: ContainsAny, lowerASCII, one Index pass per word  once per call; 27 passes over the text (constant factor)
//	wordClass, keyClass: tail and segment loops                  constant (maxIdent) per candidate; at most 4 candidates per byte
//	maskKV: name start loop                                      constant (maxIdent)
//	maskKV: name end loop                                        charged; remembered for every word of the same name
//	maskKV: readValue per word                                   charged; a name is read once per class (at most 4 times)
//	readValue: first space loop (a return false exit)            consumed (the stretch ends at the next non-space; no later key reads it again)
//	readValue: backslash, space and tab loops, next-line skips   charged (see)
//	readValue: looksLikeKey on the next line                     constant (maxIdent+2 bytes), from a step that is charged
//	readValue: comment lines before a value                      at most 4 lines, each ending at the remembered line end (charged)
//	closingQuote, bracketEnd, braced scan, unquotedEnd           charged; the position moves to their end (consumed)
//	nextIsSetting, entityEnd, quoteEndsText                      constant (maxIdent, 7, one word) per call, from a step that is charged
//	blockHeader: tag and anchor loops                            constant (maxIdent each, at most 2); every exit is charged for what it read
//	blockHeader: comment to the end of the line                  charged once per line (remembered line end)
//	blockText: key line start and key line indent                remembered and charged (lineStart, lineIndent)
//	blockText: lines of the block, TrimRight, indent loop        charged per line; stops over budget
//	maskXML: IndexByte for the next "<"                          consumed (the position moves past the tag name)
//	maskXML: tag name loop                                       constant (maxIdent)
//	maskXML: attribute loops                                     bounded by the tag (maxTag) and charged; a tag that parses is consumed
//	maskXML: keyClass, findWords on attribute names and values   constant factor per byte of the tag (about 27 index passes); the tag is charged, these are not
//	maskXML: IndexByte for the "<" after a cut tag               charged; consumed (pos moves to it)
//	maskXML: onlySpace                                           stops at the first non-space; the text node scan reads the same run once
//	parseTag: all loops and the quote search                     limited to maxTag; charged (a failed read is charged up to its limit)
//	parseTag: attrs                                              built once per tag into one buffer that the next tag reuses: a tag that does not close allocates nothing
//	textNode: space skip, text, CDATA, comment, tag              charged; comment and tag searches limited to maxTag; consumed on success
//	textNode: CDATA "]]>" search                                 unbounded, but both outcomes move the position to the end of what it read (consumed)
//	MaskURLUserinfo: Index for "://", authority loop             charged; consumed (the position moves to the end of the authority)
//	MaskURLUserinfo: login search past "/", "?", "#"             limited to loginReach (512); charged
//	MaskURLUserinfo: LastIndexByte, IndexAny on the authority    inside the authority, which is consumed
//	work.lineEnd, lineStart, lineIndent                          charged; each answer is remembered for the keys that follow
//	Carry.Line: TrimRight, indentOf, HasPrefix, plausibleValue   once per line, linear in the line (plausibleValue reads at most 512 bytes)
//	apply, sortEdits                                             once per call, linear (sort is n log n in the number of edits)
package secretscrub
