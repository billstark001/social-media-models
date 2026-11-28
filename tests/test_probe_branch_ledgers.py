"""Public wire/typed API preserves independent ledgers and output availability."""
from collections import Counter

import pytest

from probe_analysis_fixtures import analysis_run, probe_binary
from smp_bindings import ProbeClient, analyze_run
from smp_bindings.domain import ProbeResponse, parse_dataclass, to_wire


def test_p0_ledgers_roundtrip_and_side_zero_are_not_lost(probe_binary, analysis_run):
  result = analyze_run(analysis_run, epsilon=.65, bridge_cores=[[0], [26]],
                       bridges={"epsilon": .65, "branch_sides": True},
                       output_stride=3, binary_path=probe_binary)
  a = result['analysis']
  assert a['branch_ledger_version'] == 1
  assert a['events_included'] and a['episodes_included']
  summaries = a['branch_summaries']
  assert len(summaries) == 12  # pair any+2 sides x 3, union any x 3
  assert {(s['scope'], s['channel'], s.get('side')) for s in summaries} == {
      *{('pair', c, side) for c in ('structural', 'opinion', 'post') for side in (None, 0, 1)},
      *{('union', c, None) for c in ('structural', 'opinion', 'post')},
  }
  for s in summaries:
    assert s['population'] == 53 and s['known_holder_rounds'] == 371
    assert s['unknown_holder_rounds'] == 0
    assert s['active_holder_rounds']+s['never_seen_holder_rounds']+s['seen_inactive_holder_rounds'] == 371
    assert (s['losses'], s['regenerations']) == ((0, 0) if s['channel'] == 'structural' else (1, 1))
    assert s['active_holder_rounds'] == (7 if s['channel'] == 'structural' else 6)
  assert to_wire(parse_dataclass(ProbeResponse, result)) == result
  # JSONL and MsgPack expose the same flattened branch structures.
  with ProbeClient(probe_binary) as client:
    jsonl = analyze_run(analysis_run, epsilon=.65, bridge_cores=[[0], [26]],
                         bridges={"epsilon": .65, "branch_sides": True}, output_stride=3, client=client)
  assert jsonl == result
  # Existing collections remain the U ledger, never a mixture of channels.
  assert Counter(e['type'] for e in a['bridge_events']) == {'initial_present': 2, 'loss': 2, 'regeneration': 2}
  assert all('channel' not in row for row in a['bridge_events']+a['bridge_episodes']+a['summaries'])


@pytest.mark.parametrize('events,episodes', [(False, True), (True, False), (False, False)])
def test_output_switches_cover_branch_lists_but_keep_risk_summaries(probe_binary, analysis_run, events, episodes):
  result = analyze_run(analysis_run, epsilon=.65, bridge_cores=[[0], [26]],
                       collection={"profile": "audit", "products": ["summaries", "snapshots"]
                                   + (["events", "conversions", "group_events"] if events else [])
                                   + (["episodes"] if episodes else [])}, binary_path=probe_binary)
  a = result['analysis']
  assert a['events_included'] is events and a['episodes_included'] is episodes
  assert bool(a.get('bridge_events')) is events and bool(a.get('branch_events')) is events
  assert bool(a.get('bridge_episodes')) is episodes and bool(a.get('branch_episodes')) is episodes
  assert len(a['branch_summaries']) == 6
  assert to_wire(parse_dataclass(ProbeResponse, result)) == result


def test_typed_reader_keeps_previous_u_only_files_readable(probe_binary, analysis_run):
  result = analyze_run(analysis_run, epsilon=.65, binary_path=probe_binary)
  a = result['analysis']
  for key in ('branch_ledger_version', 'branch_events', 'branch_episodes', 'branch_summaries', 'events_included', 'episodes_included'):
    a.pop(key, None)
  a['bridge_options'].pop('branch_sides', None)
  for frame in result['results']:
    if 'bridges' in frame:
      frame['bridges'].pop('branch_ledger_version', None)
  assert to_wire(parse_dataclass(ProbeResponse, result)) == result
