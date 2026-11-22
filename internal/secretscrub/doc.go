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
//     runs to the end of the text, so nothing after it is kept; a value that may hold spaces runs to the next ";" or line end.
//   - Ordinary text stays readable. A key is a name followed by "=" or ":", so the sentence "the password was reset" is not touched,
//     and names that only contain the word as a prefix of another word (tokenizer, secretary) or end in a size or a policy word
//     (token_count, password_min_length, max_tokens) are left alone.
//   - Not covered (out of scope, listed so nobody assumes it): entity-escaped markup (&lt;Password&gt;), \u-escaped key names,
//     zero-width or full-width letters in a key, URL-encoded "password%3Dx", headers such as Ocp-Apim-Subscription-Key and a SAS "sig="
//     parameter, "--password x" flags (the Releases reader has its own pattern), and text nodes after the first in <Password>a<b/>c</Password>.
//   - Linear time. No regular expression here backtracks and every look-ahead is bounded or consumes what it reads, so one hostile
//     megabyte cannot cost more than a few hundred milliseconds.
//
// The key stays in the text and the value (or, for a quoted value, the text inside the quotes) becomes the mask; the callers keep
// their own older patterns and run them after Mask.
package secretscrub
