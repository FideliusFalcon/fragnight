// Live lobby list. All text is set with textContent, never innerHTML.
(function () {
  const rows = document.getElementById('lobby-rows');
  const empty = document.getElementById('empty');
  const joinBtn = document.getElementById('join');
  const count = document.getElementById('count');
  let selected = null;
  let lobbies = [];
  let deadlines = {};

  const label = {
    waiting: 'Waiting', voting: 'Voting', awaiting_server: 'Waiting for a free server',
    in_match: 'In match', finished: 'Finished',
  };

  function statusText(l) {
    let t = label[l.status] || l.status;
    if (l.status === 'voting') {
      const left = Math.max(0, Math.ceil((deadlines[l.id] - Date.now()) / 1000));
      t += ' 0:' + String(left).padStart(2, '0');
    }
    if (l.status === 'in_match') t += ' ' + l.score[0] + '–' + l.score[1];
    return t;
  }

  function cell(text) {
    const td = document.createElement('td');
    td.textContent = text;
    return td;
  }

  function render() {
    rows.replaceChildren();
    for (const l of lobbies) {
      const tr = document.createElement('tr');
      tr.className = 'lrow' + (l.status === 'in_match' ? ' live' : '') + (l.id === selected ? ' sel' : '');
      tr.dataset.id = l.id;
      const dot = document.createElement('td');
      const span = document.createElement('span');
      span.className = 'dot ' + l.status;
      dot.appendChild(span);
      tr.append(dot, cell(l.name), cell(l.host_name), cell(l.on_teams + ' / ' + l.capacity + (l.players > l.on_teams ? '  +' + (l.players - l.on_teams) : '')), cell(l.map_info), cell(statusText(l)));
      tr.addEventListener('click', () => { selected = l.id; joinBtn.disabled = false; render(); });
      tr.addEventListener('dblclick', () => { location.href = '/lobby/' + encodeURIComponent(l.id); });
      rows.appendChild(tr);
    }
    empty.textContent = lobbies.length ? '' : 'No public lobbies yet. Create one!';
    count.textContent = lobbies.length + ' public lobb' + (lobbies.length === 1 ? 'y' : 'ies') + ' · private lobbies are joined by link';
    if (selected && !lobbies.some((l) => l.id === selected)) { selected = null; joinBtn.disabled = true; }
  }

  function connect() {
    const ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws/browser');
    ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.type !== 'list') return;
      lobbies = m.lobbies || [];
      deadlines = {};
      for (const l of lobbies) deadlines[l.id] = Date.now() + (l.vote_remaining_ms || 0);
      render();
    };
    ws.onclose = () => setTimeout(connect, 2000);
  }

  joinBtn.addEventListener('click', () => { if (selected) location.href = '/lobby/' + encodeURIComponent(selected); });
  document.getElementById('refresh').addEventListener('click', () => location.reload());
  const dlg = document.getElementById('create-dialog');
  document.getElementById('create').addEventListener('click', () => dlg.showModal());
  document.getElementById('create-cancel').addEventListener('click', () => dlg.close());

  // initial state from the server-rendered rows, then live updates
  for (const tr of rows.querySelectorAll('tr')) {
    tr.addEventListener('click', () => { selected = tr.dataset.id; joinBtn.disabled = false; });
    tr.addEventListener('dblclick', () => { location.href = '/lobby/' + encodeURIComponent(tr.dataset.id); });
  }
  connect();
  setInterval(() => { if (lobbies.some((l) => l.status === 'voting')) render(); }, 1000);
})();
