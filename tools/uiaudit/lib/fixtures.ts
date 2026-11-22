// Tiny made-up files the audit hands to the real file input of the add-files screen (step `upload`). Nothing here is real data:
// the names, computers and addresses are invented, and the files are generated in memory, so nothing is written to disk.
export interface Upload { name: string; mimeType: string; buffer: Buffer }

const text = (name: string, body: string[]): Upload => ({ name, mimeType: 'text/plain', buffer: Buffer.from(body.join('\n') + '\n', 'utf8') });

/** An IIS web log (W3C) written by the computer `host`. */
function w3c(name: string, host: string): Upload {
  const row = (time: string, uri: string, status: number, took: number) => `2026-10-20 ${time} W3SVC1 ${host} GET ${uri} - 443 - ${status} 0 0 ${took}`;
  return text(name, [
    '#Software: Microsoft Internet Information Services 10.0',
    '#Version: 1.0',
    '#Date: 2026-10-20 08:00:00',
    '#Fields: date time s-sitename s-computername cs-method cs-uri-stem cs-uri-query s-port cs-username sc-status sc-substatus sc-win32-status time-taken',
    row('08:00:06', '/api/v1/orders/1', 200, 125),
    row('08:00:08', '/api/v1/orders/2', 200, 310),
    row('08:00:10', '/api/v1/orders/3', 500, 98)
  ]);
}

/** The block log a log4net setup keeps. The file name says nothing about what it is; the first lines do. */
const log4net = (name: string): Upload => text(name, [
  'Logger name: Synthetic.Api.OrdersController',
  'Type: ERROR',
  'Server Time: 2026-10-20 08:00:09.013',
  'UTC Time: 2026-10-20 08:00:09.013',
  'Application URL: https://app.example.test/api/orders/3',
  'User IP: 192.0.2.11',
  'Order 3 could not be saved',
  ''
]);

/** Fixtures by key. A step names the keys; the file name is what the screen shows. */
export const FIXTURES: Record<string, () => Upload> = {
  'iis-web-01': () => w3c('u_ex261020.log', 'WEB-01'),
  'iis-web-02': () => w3c('u_ex261021.log', 'WEB-02'),
  'app-no-name': () => log4net('orders.txt'),
  'httperr': () => text('httperr1.log', [
    '#Software: Microsoft HTTP API 2.0',
    '#Version: 1.0',
    '#Date: 2026-10-20 08:00:00',
    '#Fields: date time c-ip c-port s-ip s-port cs-version cs-method cs-uri sc-status s-siteid s-reason s-queuename',
    '2026-10-20 08:00:10 192.0.2.50 51000 192.0.2.10 443 HTTP/1.1 GET /api/v1/orders/42 503 1 QueueFull DefaultAppPool'
  ]),
  'sql-errorlog': () => text('ERRORLOG', ['2026-10-20 08:00:01.10 spid12s     Made-up line: the database was started.']),
  'notes': () => text('notes.txt', ['Made-up note: nothing in here says what kind of log it is.']),
  'binary': () => ({ name: 'blob.bin', mimeType: 'application/octet-stream', buffer: Buffer.from([0, 255, 1, 254, 2, 253, 3, 252, 0, 0, 7, 8]) }),
  'compressed': () => ({ name: 'logs-october.gz', mimeType: 'application/gzip', buffer: Buffer.from([0x1f, 0x8b, 8, 0, 0, 0, 0, 0]) }),
  'empty': () => ({ name: 'empty.log', mimeType: 'text/plain', buffer: Buffer.alloc(0) })
};

export function makeUploads(keys: string[]): Upload[] {
  return keys.map((k) => {
    const make = FIXTURES[k];
    if (!make) throw new Error('Unknown fixture: ' + k);
    return make();
  });
}
