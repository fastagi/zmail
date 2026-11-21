#!/usr/bin/env python3
"""Compare provider-required mail DNS with public DNS; never claim receiver PASS."""
import argparse
import json
import shlex
import subprocess
import sys
from datetime import datetime, timezone


def txt(value):
    return ''.join(shlex.split(value)) if value.startswith('"') else value


def records_at(name, kind):
    result = subprocess.run(['dig', '+short', '+time=3', '+tries=2', kind, name],
                            check=True, capture_output=True, text=True, timeout=10)
    lines = result.stdout.splitlines()
    if kind == 'TXT':
        return [txt(line) for line in lines if line.startswith('"')]
    if kind == 'MX':
        return [' '.join(line.lower().rstrip('.').split()) for line in lines]
    return [line.lower().rstrip('.') for line in lines]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--domain', required=True)
    parser.add_argument('--records', required=True, help='Cloudflare required DNS records JSON')
    args = parser.parse_args()
    with open(args.records, encoding='utf-8') as stream:
        required = json.load(stream)
    if isinstance(required, dict):
        required = required['result']
    checks = []
    for record in required:
        name, kind = record['name'].rstrip('.'), record['type'].upper()
        if name != args.domain and not name.endswith('.' + args.domain):
            raise ValueError('Required record is outside requested domain')
        observed = records_at(name, kind)
        expected = txt(record['content']) if kind == 'TXT' else record['content'].lower().rstrip('.')
        if kind == 'MX':
            expected = str(record['priority']) + ' ' + expected
        checks.append({'name': name, 'type': kind, 'matches_provider': expected in observed,
                       'expected': expected, 'observed': observed})
    dmarc = [r for r in records_at('_dmarc.' + args.domain, 'TXT') if r.startswith('v=DMARC1;')]
    tags = dict(part.strip().split('=', 1) for part in dmarc[0].split(';') if '=' in part) if len(dmarc) == 1 else {}
    enforcement = tags.get('p') in ('quarantine', 'reject') and tags.get('pct', '100') == '100'
    report = {'checked_at': datetime.now(timezone.utc).isoformat(), 'domain': args.domain,
              'checks': checks, 'dmarc_enforced': enforcement,
              'dns_ready': bool(checks) and all(r['matches_provider'] for r in checks) and enforcement,
              'receiver_authentication': 'Not verified by DNS inspection; inspect received Authentication-Results.',
              'inbox_placement': 'Not established by DNS inspection or provider delivered events.'}
    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 0 if report['dns_ready'] else 1


if __name__ == '__main__':
    sys.exit(main())
