/**
 * Help layer copy. Each key is an id a screen passes to <HelpNote id="...">.
 * `plain` is the one-sentence line that is always visible. `simple` is the "In simple words" block:
 * two sentences at most, with an everyday comparison.
 * Meanings come from the glossary and the existing hints, so help never contradicts them. No new terms are defined here,
 * and the copy lint (cause, because, due to, waiting on, blocked by, led to, result in, proof) applies to every string.
 * Field keys are the element ids of the More filters panel; the six range rows are rf-{RangeKey}.
 */
export const help: Record<string, { plain: string; simple: string }> = {
  // Filter groups
  when: {
    plain: 'Pick the stretch of time to look at, and an earlier stretch to compare it with.',
    simple: 'It is like choosing which pages of a diary to read. The earlier period is the same diary from a week before, so you can spot what changed.'
  },
  what: {
    plain: 'Narrow the requests by their status, completion, name and request type.',
    simple: 'It is like sorting a pile of mail into smaller piles before you read it. Only the pile you pick is counted.'
  },
  details: {
    plain: 'Match requests by who sent them, where they came from and what the record says.',
    simple: 'It is like finding one customer\'s receipts in a drawer full of receipts. Every box you fill in has to match.'
  },
  ranges: {
    plain: 'Keep only the requests whose time or size falls between a minimum and a maximum.',
    simple: 'It is like a height check at a ride, where you keep only people between the two marks. Leave a box empty and that end stays open.'
  },
  chart: {
    plain: 'Choose what the chart measures, how wide each bar is, and an optional target line.',
    simple: 'It is like choosing the units on a ruler and how often to mark it. The target line is a mark you draw yourself to compare against.'
  },

  // When
  'current-range': {
    plain: 'The period you want to inspect.',
    simple: 'It is the stretch of time you put under the magnifying glass. The app counts the requests that started inside it.'
  },
  'reference-range': {
    plain: 'An earlier period to compare with.',
    simple: 'It is like last week\'s weather next to this week\'s. It does not mean the earlier period was normal or healthy.'
  },
  'current-start': {
    plain: 'The time the period you want to inspect begins.',
    simple: 'It is the moment you press start on a stopwatch. Requests that started earlier are left out.'
  },
  'current-end': {
    plain: 'The time the period you want to inspect ends.',
    simple: 'It is the moment you press stop on the stopwatch. Requests that started later are left out.'
  },
  'reference-start': {
    plain: 'The time the earlier comparison period begins.',
    simple: 'It is the first minute of the older recording you hold up next to the new one. Requests that started earlier are left out.'
  },
  'reference-end': {
    plain: 'The time the earlier comparison period ends.',
    simple: 'It is the last minute of the older recording. Requests that started later are left out.'
  },

  // What
  outcome: {
    plain: 'Show only requests that succeeded or failed, or only 4xx or 5xx status codes.',
    simple: 'It is like sorting letters into delivered and returned. A 4xx means the request was refused as invalid, and a 5xx means the server reported a failure.'
  },
  'rf-completion': {
    plain: 'Show requests by whether they finished, have no finish recorded, or are still pending.',
    simple: 'It is like checking which parcels have been signed for. A request with no finish recorded may simply have been recorded late.'
  },
  controller: {
    plain: 'The name of the part of the API that handles a request.',
    simple: 'It is like the department name on a letter. Leave it empty to include every department.'
  },
  action: {
    plain: 'The name of one job inside a controller.',
    simple: 'If the controller is a department, the action is one desk in it. Together they name one API action.'
  },
  'rf-operation': {
    plain: 'Name the controller and the action together, with a dot between them.',
    simple: 'It is like a street name followed by a house number. The suggestions come from the requests already loaded.'
  },
  'kind-unknown': {
    plain: 'Show only HTTP requests whose polling type is not recorded.',
    simple: 'Some unfinished requests have no polling type yet, like a parcel with no label. They cannot be counted as Synchronous or Asynchronous until the audit update is written.'
  },

  // Request details
  'rf-user-ids': {
    plain: 'Show only requests made by the user numbers you list.',
    simple: 'It is like asking for the receipts of certain customer numbers. Separate several numbers with commas.'
  },
  'rf-ui-versions': {
    plain: 'Show only requests sent from the exact app versions you list.',
    simple: 'It is like asking for letters from one edition of a book. The text has to match exactly.'
  },
  'rf-user-ips': {
    plain: 'Show only requests that came from the exact network addresses you list.',
    simple: 'A network address is like a home address for a computer. The text has to match exactly.'
  },
  'rf-status-codes': {
    plain: 'Show only requests that ended with the status codes you list.',
    simple: 'A status code is a short number the server sends back, like a stamp on a letter. For example, 200 usually means all went well and 500 means the server reported a failure.'
  },
  'rf-agent': {
    plain: 'Show only requests whose browser name contains the text you type.',
    simple: 'It is like searching a guest list for part of a name. Capital letters do not matter.'
  },
  'rf-url': {
    plain: 'Show only requests whose web address contains the text you type.',
    simple: 'It is like searching a book for a word. Capital letters do not matter.'
  },
  'rf-audit-id': {
    plain: 'Find one request by its audit ID, a code of 24 letters and digits.',
    simple: 'It is like the tracking number on a parcel. Paste the whole code to go straight to that one record.'
  },

  // Ranges
  'rf-observedMs': {
    plain: 'Keep requests whose observed time falls between the limits you set.',
    simple: 'Observed time runs from the request start to its completion, like a stopwatch from the first click to the page loading. It only applies to requests that finished.'
  },
  'rf-apiTimeMs': {
    plain: 'Keep requests whose API time falls between the limits you set.',
    simple: 'API time is the time the action itself took to run, like the time a cook spends cooking without the order being taken. It does not show where inside the action the time went.'
  },
  'rf-auditCreateTimeMs': {
    plain: 'Keep requests whose audit write time falls between the limits you set.',
    simple: 'It is the time from the request starting to its audit record being saved, like the time from handing in a form to getting the receipt stamped. It is measured from the application side.'
  },
  'rf-requestKb': {
    plain: 'Keep requests whose request body is within the size limits you set.',
    simple: 'The request body is what was sent in, like the weight of a parcel. A request that does not record a size never matches a bounded range.'
  },
  'rf-responseKb': {
    plain: 'Keep requests whose response body is within the size limits you set.',
    simple: 'The response body is what was sent back, like the weight of the reply parcel. A request that does not record a size never matches a bounded range.'
  },
  'rf-incompleteAgeMs': {
    plain: 'Keep requests that have gone without finishing for the length of time you set.',
    simple: 'It is like counting how long a parcel has been out for delivery with no signature. It is measured when the search runs, and it cannot be combined with a time range.'
  },

  // Chart
  metric: {
    plain: 'Choose which time the chart and the figures measure.',
    simple: 'It is like choosing whether to time a whole trip or only the driving. One option adds the audit write time to the API time, and the other counts the API action only.'
  },
  bucket: {
    plain: 'Set how many seconds each bar or dot on the chart covers.',
    simple: 'It is like choosing whether a bar chart shows each minute or each hour. A smaller number shows more detail, up to 120 points.'
  },
  'latency-target': {
    plain: 'Draw a line on the chart at a response time you care about.',
    simple: 'It is like marking a finish line you want runs to beat. The app compares the line with P95, the time within which about 95 of every 100 requests finished.'
  },

  // Drill-down
  'drill-navigator': {
    plain: 'The strip under the chart shows the whole period, and the highlighted part is what you are looking at.',
    simple: 'It is like the zoom bar on a map. Drag the edges to see more or less, and drag the middle to slide earlier or later.'
  },
  'drill-investigate': {
    plain: 'Run the analysis again on only the part of the chart you are looking at.',
    simple: 'It is like cutting one piece out of a photo and studying just that. The rest of the period is set aside until you go back.'
  },
  'drill-back': {
    plain: 'Go back to the view you had before the last zoom.',
    simple: 'It works like the back button after you follow a link. Each press takes you one step back.'
  },

  // Screens and drawers
  pulse: {
    plain: 'Five headline figures and one timeline give the quick picture of the period.',
    simple: 'It is like the dashboard of a car, with a few dials and one line for the trip so far. Press a figure to open the ranked list for it.'
  },
  where: {
    plain: 'Operations ranked by the figure you chose, so you can open one and see the evidence behind it.',
    simple: 'It is like a list of shops sorted by how busy each one was. Open one to look through its receipts.'
  },
  inspector: {
    plain: 'Shows the evidence for one operation: when it ran, which calls were slow, and the records.',
    simple: 'It is like opening the file for one shop. The tabs show its busy hours, its slowest customers and the receipts.'
  },
  resource: {
    plain: 'What each host measured around the stalls: CPU, memory, queues and logs.',
    simple: 'It is like the gauges on a machine, read at the same time as the slow stretch. They show what was measured, and they do not say why requests were slow.'
  },
  patterns: {
    plain: 'Looks at the slowest requests in the window and groups the ones that happened close together.',
    simple: 'Each group is an episode, like a cluster of raindrops on a window. Several separate episodes may mean the same thing keeps happening.'
  },
  ledger: {
    plain: 'A list of ideas to check, each with its evidence, kept in a local file.',
    simple: 'It is like a detective\'s notebook where each guess has its clues pinned beside it. An entry is an idea to test, not a finding.'
  },
  export: {
    plain: 'Build a file of the findings in this window, and preview it before anything is saved.',
    simple: 'It is like packing a folder to hand to a colleague, with a look inside first. Nothing is saved until you have seen the whole preview.'
  },
  import: {
    plain: 'Add files exported from your servers to a case, where they stay on this computer.',
    simple: 'It is like putting papers into a locked drawer in your own office. The files are not sent anywhere, and the server they came from is not changed.'
  },
  discover: {
    plain: 'Look on this computer for logs and data that Unravis can import.',
    simple: 'It is like asking a librarian what is on the shelf before you borrow any book. It reads file names and sizes first and opens nothing until you choose.'
  },
  start: {
    plain: 'Pick a saved case, enter a case you were given, or begin a new one.',
    simple: 'It is like the front desk of an archive, where you can ask for a folder you already have or start a fresh one. Everything stays on this computer.'
  },
  enter: {
    plain: 'Type the code of a case on this computer, or restore a case from a case file as a new case.',
    simple: 'It is like giving the archive clerk a folder number to fetch a folder. A case file is a sealed copy of a folder, and opening it always makes a new folder, so the old one stays as it was.'
  },
  wizard: {
    plain: 'Name your case, choose what to look at, then check it all before the case is created.',
    simple: 'It is like writing a label for a new folder and then picking which papers go inside. Nothing goes into the folder until you press create at the end.'
  },
  'wizard-envs': {
    plain: 'An environment is one copy of your system, such as production or a client sandbox.',
    simple: 'It is like giving each building of a company its own name, so you can tell which building a report came from. You can skip this and add names later.'
  },
  'wizard-sources': {
    plain: 'Choose which logs and data to look at, and name each one by environment, portal and API.',
    simple: 'It is like sorting your papers into labelled piles before you start. A pile with no label still works, and you can name it later.'
  },
  'wizard-review': {
    plain: 'Check the summary, tick the notice if logs will be read, then create the case.',
    simple: 'It is like reading your order before you pay. Once you press create, the folder is made and the papers you chose are filed into it, and anything that fails is kept aside so you can try it again.'
  },
  masterkey: {
    plain: 'A master key locks your saved cases on this computer, and it is never sent anywhere.',
    simple: 'It is like the one key to a filing cabinet that you keep in your own pocket. If you lose it, nobody can open the cabinet for you.'
  },
  unlock: {
    plain: 'Type your master key to open your saved cases for this session.',
    simple: 'It is like unlocking the filing cabinet once when you arrive. You only do it again the next time you start the app.'
  }
};

/**
 * Words for the user guide inside the Help drawer. The guide pages themselves are the Markdown files in docs/user.
 * These keys sit here, next to the other help copy, and the copy lint applies to every string.
 * (HELP_TEXT in screenCopy.ts holds the drawer title and the start steps.)
 */
export const HELP_GUIDE_TEXT = {
  guideButton: 'Open the user guide',
  guideBack: 'Back to help',
  guideContents: 'Pages',
  guideImage: 'Picture: ',
  guideMissing: 'This page is not available.'
} as const;
