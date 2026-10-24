import type { Deck } from './types';

/**
 * The single reviewable copy deck (roadmap §8.14). One entry per technical word a reader meets on screen.
 * Writing rules: 8th-grade reading level, short active sentences, a one-sentence hint, a two-sentence
 * definition and a "what it does not mean" line. Never name a cause for a measurement.
 */
export const deck: Deck = {
  terms: [
    {
      id: 'p50',
      term: 'P50',
      hint: 'The typical time: half of the requests finished faster than this.',
      definition: 'P50 is the middle response time of a group of requests. Half of them finished faster and half took longer.',
      notMeaning: 'It does not describe the slowest requests, which can be much slower than this.',
      aliases: ['Median', 'Typical time (median)']
    },
    {
      id: 'p90',
      term: 'P90',
      hint: 'The time within which about 90 of every 100 requests finished.',
      definition: 'P90 shows how long the slower end of a group of requests took. The slowest 10 in every 100 took longer than this.',
      notMeaning: 'It does not mean every request was this slow, and it is an estimate read from a histogram.'
    },
    {
      id: 'p95',
      term: 'P95',
      hint: 'The time within which about 95 of every 100 requests finished.',
      definition: 'P95 shows how slow the slower end of a group of requests was. The slowest 5 in every 100 took longer than this.',
      notMeaning: 'It does not mean every request was this slow, and it is not a verdict on health.',
      aliases: ['Slower-end time (P95)', 'Percentile']
    },
    {
      id: 'p99',
      term: 'P99',
      hint: 'The time within which about 99 of every 100 requests finished.',
      definition: 'P99 looks even further into the slow end than P95. Only the slowest 1 in every 100 took longer than this.',
      notMeaning: 'It does not mean typical requests are slow, and a few requests can move it a lot.'
    },
    {
      id: 'response-time',
      term: 'Response time',
      hint: 'How long the app recorded for one request.',
      definition: 'Response time is the length of time the application recorded for a request. The app shows it in milliseconds or seconds.',
      notMeaning: 'It does not include time spent on the user side, such as the browser or the network.',
      aliases: ['Duration', 'Latency']
    },
    {
      id: 'api-time',
      term: 'API time',
      hint: 'The time the audit record says the API action itself took to run.',
      definition: 'API time is one duration stored in the audit record of a request. It covers the action, not the steps before or after it.',
      notMeaning: 'It does not show where inside the action the time went.',
      aliases: ['API ms', 'API / audit time']
    },
    {
      id: 'audit-create-time',
      term: 'Audit create time',
      hint: 'Time from the request starting to its audit record being saved, as measured from the application side.',
      definition: 'Audit create time is the time the record was saved minus the time the request began. It includes driver, connection pool and network time as well as the database.',
      notMeaning: 'It does not show where the time went, so it does not point at the database or at anything else.',
      aliases: ['Audit create ms', 'Saving the record']
    },
    {
      id: 'coverage',
      term: 'Coverage',
      hint: 'How much of the expected data the app actually received for this period.',
      definition: 'Coverage tells you how complete the records are. Low coverage means some activity may be missing from what you see.',
      notMeaning: 'It does not mean the missing activity was quiet or went fine.',
      aliases: ['Incomplete source coverage', 'Source coverage']
    },
    {
      id: 'episode',
      term: 'Episode',
      hint: 'One stretch of time when a slow pattern showed up in the records.',
      definition: 'An episode is one continuous period of the same slowdown. A burst of many requests in a short time still counts as one episode.',
      notMeaning: 'One episode is not yet a pattern, and it does not tell you why it happened.',
      aliases: ['Independent episode']
    },
    {
      id: 'correlation',
      term: 'Correlation',
      hint: 'Two things happened at about the same time.',
      definition: 'A correlation means two measurements appeared together in the same period. It is a clue to look closer, not an answer.',
      notMeaning: 'It does not show that one made the other happen.'
    },
    {
      id: 'strong-correlation',
      term: 'Strong correlation',
      hint: 'Two things showed up together in at least 3 separate episodes, with the exceptions listed.',
      definition: 'A strong correlation is a correlation seen in at least 3 separate episodes. The entry says how often the two came together, lists the cases that did not fit and names other ways to explain it.',
      notMeaning: 'It does not show that one made the other happen, however often they appear together.'
    },
    {
      id: 'symptom',
      term: 'Symptom',
      hint: 'Something the records show, such as a long time, with no link to anything else.',
      definition: 'A symptom is a single observation, like one slow request. It is the lowest evidence label the app uses.',
      notMeaning: 'It does not point to a reason, and it does not mean something is broken.'
    },
    {
      id: 'evidence-label',
      term: 'Evidence label',
      hint: 'A tag that says how much support the records give to a statement.',
      definition: 'Each finding has one of three labels: Symptom, Correlation or Strong correlation. The label shows how well the records back the statement.',
      notMeaning: 'It does not rate how serious a finding is, and none of the labels mean certain.',
      aliases: ['Evidence']
    },
    {
      id: 'join',
      term: 'Join',
      hint: 'How the app matches a record from one source to a record from another.',
      definition: 'A join links two records that share an identifier or overlap in time. Some joins match one record and some match several.',
      notMeaning: 'A match is a link between records, not a statement that one made the other happen.',
      aliases: ['Joined', 'Match']
    },
    {
      id: 'not-observed',
      term: 'Not observed',
      hint: 'The source shows no event here, but it may not be able to record one.',
      definition: 'This label means the source has no record for this moment. The source may skip short events, have gaps or keep records for a short time.',
      notMeaning: 'It does not mean nothing happened, and it does not rule anything out.',
      aliases: ['Sampling', 'Gap or sampling']
    },
    {
      id: 'time-outside-the-pipeline',
      term: 'Time outside the pipeline',
      hint: 'Time that one source measured but the application records do not account for.',
      definition: 'This is the web server time minus the time the application recorded. The difference can include the network and the time to send the reply.',
      notMeaning: 'It does not mean the request sat in a queue, and it does not show where the time went.'
    },
    {
      id: 'low-sample',
      term: 'Low sample',
      hint: 'There are too few requests here to trust the numbers much.',
      definition: 'A low sample means only a small number of requests went into a figure. A few unusual requests can then move the figure a lot.',
      notMeaning: 'It does not mean the figure is wrong, only that it can change easily.',
      aliases: ['Small sample', 'Sample size']
    },
    {
      id: 'reference-period',
      term: 'Reference period',
      hint: 'An earlier stretch of time that the current period is compared with.',
      definition: 'The reference period is the baseline the app measures change against. You can pick it, and it can come from a different time of day or week.',
      notMeaning: 'It does not mean the reference period was normal or healthy.',
      aliases: ['Reference', 'Baseline', 'Compare to earlier']
    },
    {
      id: 'slowdown-ratio',
      term: 'Slowdown ratio',
      hint: 'How many times longer the current period took than the reference period.',
      definition: 'A ratio of 2 means the time doubled and a ratio of 1 means no change. A ratio below 1 means the time got shorter.',
      notMeaning: 'It does not say why the time changed, and it is not a health verdict.',
      aliases: ['Ratio', 'Change versus reference', 'Largest slowdown']
    },
    {
      id: 'time-window',
      term: 'Time window',
      hint: 'The start and end times of the period you are looking at.',
      definition: 'A time window is a fixed slice of the calendar, such as five minutes. The app counts the requests that started inside it.',
      notMeaning: 'It does not include activity outside its start and end times.',
      aliases: ['Window', 'Plotted time']
    },
    {
      id: 'time-slice',
      term: 'Time slice',
      hint: 'One small piece of the chart interval, shown as a single bar or dot.',
      definition: 'A time slice is one equal piece of the period on the chart. Its size is the chart interval you choose.',
      notMeaning: 'It does not mean the slice is a separate event, and an empty slice does not mean zero failures.',
      aliases: ['Bucket', 'Interval', 'Chart interval']
    },
    {
      id: 'histogram',
      term: 'Histogram',
      hint: 'A chart that counts requests in each response-time range.',
      definition: 'A histogram sorts requests into ranges, such as under one second or one to two seconds. The taller the bar, the more requests fell in that range.',
      notMeaning: 'It does not give exact times, and the P50 and P95 read from it are estimates.'
    },
    {
      id: 'outcome',
      term: 'Outcome',
      hint: 'Whether a request finished as a success or a failure.',
      definition: 'The outcome is the result recorded for a request. The app groups requests by outcome so successes and failures stay apart.',
      notMeaning: 'It does not say what went wrong in a failure.',
      aliases: ['Result']
    },
    {
      id: 'status-class',
      term: '4xx and 5xx',
      hint: '4xx means the request was refused as invalid; 5xx means the server reported a failure.',
      definition: 'These are two groups of status codes in the response to a request. The app counts failed background jobs with the 5xx group.',
      notMeaning: 'A 4xx is not a fault in the server, and a 5xx does not name what failed.',
      aliases: ['4xx', '5xx', 'Client errors', 'Server errors', 'Status class']
    },
    {
      id: 'synchronous',
      term: 'Synchronous',
      hint: 'A request that does all its work before it answers.',
      definition: 'A synchronous request returns its answer when the work is done. Its recorded time covers the whole job.',
      notMeaning: 'It does not mean the request was fast or slow.',
      aliases: ['Sync']
    },
    {
      id: 'asynchronous',
      term: 'Asynchronous',
      hint: 'A request that answers quickly and finishes its work later.',
      definition: 'An asynchronous request answers right away that it was accepted. The real work runs afterward as a separate job.',
      notMeaning: 'A quick answer does not mean the work has finished.',
      aliases: ['Async']
    },
    {
      id: 'hangfire',
      term: 'Hangfire',
      hint: 'The background job system that runs work after a request was accepted.',
      definition: 'Hangfire runs jobs in the background, away from the person who made the request. The app shows these jobs next to normal requests.',
      notMeaning: 'A background job is not a user request, so its time is not the time a user saw.',
      aliases: ['Background job']
    },
    {
      id: 'pending-async',
      term: 'Pending async',
      hint: 'An asynchronous request that was accepted but has no finished record yet.',
      definition: 'A pending request answered that it was accepted, and no finish was recorded. The app counts it as missing timing, never as a response-time sample.',
      notMeaning: 'It does not mean the work failed or is stuck.',
      aliases: ['Pending']
    },
    {
      id: 'incomplete-request',
      term: 'Incomplete request',
      hint: 'A request whose audit record has no finish time.',
      definition: 'An incomplete request started, but no complete duration was recorded for it. The app lists these apart so they are not mixed with finished requests.',
      notMeaning: 'It does not mean the request failed, and it may simply have been recorded late.',
      aliases: ['Incomplete', 'Incomplete requests']
    },
    {
      id: 'stale',
      term: 'Stale',
      hint: 'An unfinished request that is older than the time limit you set.',
      definition: 'A request is stale when it has no finish and is older than the limit in minutes. You can change the limit on the screen.',
      notMeaning: 'It does not mean the request is stuck, only that no finish has been recorded yet.',
      aliases: ['Stale after']
    },
    {
      id: 'unusable-timing',
      term: 'Unusable timing',
      hint: 'A duration was recorded, but the app cannot use it.',
      definition: 'An unusable timing is a duration that is missing, negative or out of range. The app counts these apart and leaves them out of every time figure.',
      notMeaning: 'It does not mean the request itself was a problem.',
      aliases: ['Unusable', 'Invalid']
    },
    {
      id: 'missing-timing',
      term: 'Missing timing',
      hint: 'A request that has no usable duration, so it cannot count toward time figures.',
      definition: 'A request has a missing timing when its record holds no complete duration. These requests still count toward request totals.',
      notMeaning: 'It does not mean the request took zero time.',
      aliases: ['Usable timings']
    },
    {
      id: 'completion-percent',
      term: 'Completion %',
      hint: 'The share of requests that finished, out of those that finished or are still open.',
      definition: 'Completion % is finished requests out of all finished, incomplete and pending ones. Rows that cannot be used are left out.',
      notMeaning: 'A dash means there was nothing to measure, not 0%.',
      aliases: ['Completion']
    },
    {
      id: 'breakdown',
      term: 'Breakdown',
      hint: 'The same requests split into groups, such as by user or by status code.',
      definition: 'A breakdown sorts the requests in view into groups and counts each group. Groups beyond the busiest few are folded into one Other row.',
      notMeaning: 'It does not rank who is at fault, and a large group may simply be a busy one.',
      aliases: ['Split']
    },
    {
      id: 'api-group',
      term: 'API group',
      hint: 'One API action, kept together with its source, request type and outcome.',
      definition: 'An API group is the unit the app compares across two periods. Each group keeps one source, one action, one request type and one outcome.',
      notMeaning: 'It does not combine different actions, so a total across groups is a sum, not an average.',
      aliases: ['API groups', 'Operation', 'Controller / action']
    },
    {
      id: 'tls-verification',
      term: 'Certificate verification',
      hint: 'A check that the database server is the one you meant to reach.',
      definition: 'Certificate verification checks the identity of the server when the app connects. If it is off, the traffic is still encrypted but the server is not checked.',
      notMeaning: 'Turning it off does not make the connection faster, and it does not make it safe.',
      aliases: ['SSL / TLS', 'Encryption and server trust']
    },
    {
      id: 'query-mode',
      term: 'Query mode',
      hint: 'How the app reads records: exact mode reads every match, hint mode reads a narrow range.',
      definition: 'Exact mode reads every record that matches the query in the period. Hint mode reads only a narrow range of the index, so it is faster.',
      notMeaning: 'It does not mean hint mode finds the same records, since it would miss any outside its range.',
      aliases: ['Exact mode', 'Hint mode']
    },
    {
      id: 'complete',
      term: 'Complete',
      hint: 'Every matching record in the period was read.',
      definition: 'Complete means the query read every matching record in the period. On a live source the app cannot count the records it read, so it says Finished instead.',
      notMeaning: 'It does not mean the records are free of gaps, and it does not rate the figures as good.',
      aliases: ['Finished']
    },
    {
      id: 'partial',
      term: 'Partial',
      hint: 'The query stopped early, so some matching records may be missing.',
      definition: 'A partial result means the query reached a time limit, a row limit or a read error. The figures cover only the records read before it stopped.',
      notMeaning: 'It does not mean the figures are wrong, only that they may be too low.'
    },
    {
      id: 'margin',
      term: 'Margin',
      hint: 'The time gap that hint mode allows between a record id and its request time.',
      definition: 'Hint mode finds records by a range of record ids. The margin widens that range by the largest gap seen in a sample of recent records.',
      notMeaning: 'It does not mean every record fits inside it, and a record outside it would be missed.'
    },
    {
      id: 'coverage-census',
      term: 'Coverage census',
      hint: 'A check of how many records have each field the app needs, by request type and day.',
      definition: 'The census reads every record in a period of up to 31 days. It counts how many have each field filled in.',
      notMeaning: 'It does not check that the values are right, and it does not run unless you ask for it.',
      aliases: ['Census']
    }
  ]
};
