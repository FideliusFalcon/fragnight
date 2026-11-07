// Live lobby page. All names and text are set with textContent, never innerHTML.
(function () {
  const root = document.getElementById('lobby');
  const lobbyID = root.dataset.id;
  const TEAM_SIZE = 5;
  const $ = (id) => document.getElementById(id);
  let state = null;
  let you = root.dataset.me;
  let deadline = 0;
  let ws = null;
  let closed = false;
  let failedConnects = 0; // consecutive connections that never delivered a state

  function send(msg) {
    if (ws && ws.readyState === 1) { ws.send(JSON.stringify(msg)); return true; }
    $('msg').textContent = 'Connection lost. Reconnecting…';
    return false;
  }
  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }

  function memberRow(m, isHost, hostID) {
    const row = el('div', 'row' + (m.steamid === you ? ' me' : ''));
    const name = el('span', m.steamid === hostID ? 'host' : '', m.name + (m.steamid === hostID ? ' ★' : ''));
    row.appendChild(name);
    const right = el('span');
    if (isHost && m.steamid !== you && (state.status === 'waiting' || state.status === 'voting')) {
      const k = el('button', 'btn', 'Kick');
      k.style.padding = '0 6px';
      k.addEventListener('click', () => send({ type: 'kick', steamid: m.steamid }));
      right.appendChild(k);
    }
    row.appendChild(right);
    return row;
  }

  function renderTeam(n, members) {
    const panel = $('team' + n);
    panel.querySelector('.count').textContent = members.length + ' / ' + TEAM_SIZE;
    const rows = panel.querySelector('.rows');
    rows.replaceChildren();
    const isHost = state.host === you;
    for (const m of members) rows.appendChild(memberRow(m, isHost, state.host));
    for (let i = members.length; i < TEAM_SIZE; i++) rows.appendChild(el('div', 'empty', '— open slot —'));
    const btn = panel.querySelector('button[data-team]');
    const mine = (state.members.find((m) => m.steamid === you) || {}).team;
    btn.disabled = state.status !== 'waiting' || mine === n || members.length >= TEAM_SIZE;
    btn.textContent = mine === n ? 'You are on Team ' + n : (members.length >= TEAM_SIZE ? 'Team full' : 'Join Team ' + n);
  }

  function renderMaps() {
    const wrap = $('maps');
    wrap.replaceChildren();
    const total = Object.values(state.tally || {}).reduce((a, b) => a + b, 0);
    const me = state.members.find((m) => m.steamid === you) || {};
    for (const name of state.settings.maps) {
      const votes = (state.tally || {})[name] || 0;
      const card = el('div', 'map' + (me.vote === name ? ' mine' : '') + (state.map === name ? ' win' : ''));
      card.dataset.map = name;
      card.appendChild(el('div', 'img', name));
      const bar = el('div', 'bar');
      const fill = document.createElement('i');
      fill.style.width = (total ? Math.round(votes * 100 / total) : 0) + '%';
      bar.appendChild(fill);
      card.append(bar, el('span', '', String(votes)));
      card.addEventListener('click', () => send({ type: 'vote', map: name }));
      wrap.appendChild(card);
    }
  }

  function renderCountdown() {
    const s = $('vote-status');
    if (!state) return;
    if (state.status === 'voting') {
      const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
      s.textContent = 'Voting ends in 0:' + String(left).padStart(2, '0');
    } else if (state.status === 'awaiting_server') {
      s.textContent = state.map ? state.map + ' · waiting for a free server' : 'waiting for a free server';
    } else if (state.status === 'in_match') {
      s.textContent = (state.map || '') + ' · ' + state.score[0] + '–' + state.score[1];
    } else if (state.status === 'finished') {
      s.textContent = 'Match finished ' + state.score[0] + '–' + state.score[1];
    } else {
      s.textContent = 'Host presses Ready to start the 30 s vote';
    }
  }

  function render() {
    if (!state) return;
    $('lobby-title').textContent = state.name + (state.public ? '' : ' (private)');
    const count = $('lobby-count');
    count.replaceChildren(document.createTextNode(state.members.length + ' players · '));
    const a = el('a', '', 'Lobby list');
    a.href = '/';
    count.appendChild(a);

    renderTeam(1, state.members.filter((m) => m.team === 1));
    renderTeam(2, state.members.filter((m) => m.team === 2));
    const un = state.members.filter((m) => m.team === 0);
    const unPanel = $('unassigned');
    unPanel.querySelector('.count').textContent = String(un.length);
    const rows = unPanel.querySelector('.rows');
    rows.replaceChildren();
    for (const m of un) rows.appendChild(memberRow(m, state.host === you, state.host));
    const leaveTeam = unPanel.querySelector('button[data-team]');
    const mine = (state.members.find((m) => m.steamid === you) || {}).team;
    leaveTeam.disabled = state.status !== 'waiting' || mine === 0;

    renderMaps();
    renderCountdown();

    const note = $('lock-note');
    const locked = state.status !== 'waiting';
    note.hidden = !locked;
    note.textContent = state.status === 'voting'
      ? 'Teams are locked during the vote. The host can stop the countdown to change them.'
      : (state.status === 'finished'
        ? 'The match is finished. The host can press Play again to reuse this lobby.'
        : (locked ? 'Teams are locked once the match is being set up.' : ''));
    for (const b of document.querySelectorAll('button[data-team]')) b.title = locked ? note.textContent : '';

    const isHost = state.host === you;
    $('host-controls').hidden = !isHost;
    const ready = $('ready-btn');
    if (state.status === 'voting') { ready.textContent = 'Stop countdown'; ready.disabled = false; }
    else { ready.textContent = 'Ready (start vote)'; ready.disabled = state.status !== 'waiting'; }
    $('settings-btn').disabled = state.status !== 'waiting';
    $('abort-btn').hidden = !(state.status === 'in_match' || state.status === 'awaiting_server');
    $('swap-btn').hidden = state.status !== 'waiting';
    $('rematch-btn').hidden = state.status !== 'finished';

    const box = $('connect-box');
    box.hidden = !state.connect;
    $('connect').textContent = state.connect || '';
    if (state.error) $('msg').textContent = state.error;
    if (state.status === 'finished' && state.match_id) {
      const msg = $('msg'); msg.replaceChildren();
      const link = el('a', '', 'View the scoreboard'); link.href = '/match/' + encodeURIComponent(state.match_id);
      msg.appendChild(link);
    }
  }

  function openSettings() {
    const form = $('settings-form');
    const s = state.settings;
    form.replaceChildren();
    function field(label, input) {
      const l = el('label'); l.append(el('span', '', label), input); form.appendChild(l); return input;
    }
    function num(v, min) { const i = el('input'); i.type = 'number'; i.value = v; i.min = min; return i; }
    function chk(v) { const i = el('input'); i.type = 'checkbox'; i.checked = v; return i; }
    const mr = field('Rounds per half (MR)', num(s.mr, 1));
    const ot = field('Overtime', chk(s.overtime));
    const otr = field('Overtime rounds (even)', num(s.ot_max_rounds, 2));
    const otm = field('Overtime start money', num(s.ot_start_money, 0));
    const knife = field('Knife round', chk(s.knife_round));
    const ff = field('Friendly fire', chk(s.friendly_fire));
    const dt = field('Dead talk', chk(s.dead_talk));
    const to = field('Timeouts per team', num(s.timeouts_per_team, 0));
    const tos = field('Timeout seconds', num(s.timeout_seconds, 0));
    const active = (root.dataset.activeMaps || '').split(',').filter(Boolean);
    const other = (root.dataset.otherMaps || '').split(',').filter(Boolean);
    const known = active.concat(other);
    function mapGroup(title, names) {
      form.appendChild(el('div', 'empty', title));
      const box = el('div', 'maps');
      const checks = names.map((name) => {
        const l = el('label'); const c = chk(s.maps.includes(name)); c.dataset.map = name;
        l.append(c, el('span', '', ' ' + name)); box.appendChild(l); return c;
      });
      form.appendChild(box);
      return checks;
    }
    const activeChecks = mapGroup('Map pool: Active Duty', active);
    const otherChecks = mapGroup('Other maps', other);
    // Custom map names are for admins only (the server also enforces this).
    const custom = el('input'); custom.type = 'text'; custom.placeholder = 'de_mymap, cs_other';
    custom.value = s.maps.filter((m) => !known.includes(m)).join(', ');
    if (root.dataset.admin === '1') {
      const customLabel = el('label'); customLabel.append(el('span', '', 'Custom maps (admin only)'), custom);
      form.appendChild(customLabel);
    }
    const resetBtn = el('button', 'btn', 'Reset to Active Duty'); resetBtn.type = 'button';
    resetBtn.addEventListener('click', () => {
      activeChecks.forEach((c) => { c.checked = true; });
      otherChecks.forEach((c) => { c.checked = false; });
      custom.value = '';
    });
    form.appendChild(resetBtn);
    const actions = el('div', 'actions');
    const save = el('button', 'btn', 'Save'); save.type = 'button';
    const cancel = el('button', 'btn', 'Cancel'); cancel.type = 'button';
    actions.append(save, cancel); form.appendChild(actions);
    cancel.addEventListener('click', () => $('settings-dialog').close());
    save.addEventListener('click', () => {
      send({ type: 'settings', settings: {
        mr: +mr.value, overtime: ot.checked, ot_max_rounds: +otr.value, ot_start_money: +otm.value,
        knife_round: knife.checked, friendly_fire: ff.checked, dead_talk: dt.checked,
        timeouts_per_team: +to.value, timeout_seconds: +tos.value,
        maps: [...new Set(activeChecks.concat(otherChecks).filter((c) => c.checked).map((c) => c.dataset.map)
          .concat(custom.value.split(/[\s,]+/).map((m) => m.trim().toLowerCase()).filter(Boolean)))],
      } });
      $('settings-dialog').close();
    });
    $('settings-dialog').showModal();
  }

  function connect() {
    ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws/lobby/' + encodeURIComponent(lobbyID));
    ws.onmessage = (ev) => {
      failedConnects = 0;
      const m = JSON.parse(ev.data);
      if (m.type === 'state') {
        state = m.lobby; you = m.you || you;
        deadline = Date.now() + (state.vote_remaining_ms || 0);
        if (!state.error) $('msg').textContent = '';
        render();
      } else if (m.type === 'error') {
        $('msg').textContent = m.message;
      } else if (m.type === 'left' || m.type === 'closed') {
        closed = true; location.href = '/';
      }
    };
    ws.onclose = () => {
      if (closed) return;
      // A lobby that no longer exists (or no longer has us) refuses the socket, so
      // after a few failures stop retrying and go back to the list.
      if (++failedConnects >= 3) { closed = true; location.href = '/'; return; }
      $('msg').textContent = 'Connection lost. Reconnecting…';
      setTimeout(connect, 1500);
    };
  }

  for (const b of document.querySelectorAll('button[data-team]')) {
    b.addEventListener('click', () => send({ type: 'team', team: +b.dataset.team }));
  }
  $('ready-btn').addEventListener('click', () => send({ type: state && state.status === 'voting' ? 'stop' : 'ready' }));
  $('settings-btn').addEventListener('click', openSettings);
  $('swap-btn').addEventListener('click', () => send({ type: 'swap' }));
  $('rematch-btn').addEventListener('click', () => send({ type: 'rematch' }));
  $('abort-btn').addEventListener('click', () => { if (confirm('Abort the match? No stats or rating changes will be recorded.')) send({ type: 'abort' }); });
  $('leave').addEventListener('click', () => send({ type: 'leave' }));
  $('delete-btn').addEventListener('click', () => {
    if (confirm('Delete this lobby? Everyone in it is removed and a running match is aborted.')) send({ type: 'delete' });
  });
  $('copy-link').addEventListener('click', () => {
    if (navigator.clipboard) navigator.clipboard.writeText(location.href);
    $('msg').textContent = 'Invite link copied: ' + location.href;
  });
  connect();
  setInterval(renderCountdown, 500);
})();
