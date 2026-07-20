package main

const page = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Raft Playground</title>
<style>
  :root{
    --bg:#0f1210; --panel:#171b18; --ink:#e8e6dd; --muted:#8b9088;
    --rule:#272c26; --accent:#5cc48c; --leader:#5cc48c; --follower:#4a5560;
    --danger:#e0664f; --brass:#d0a066; --chip:#1c2a20;
  }
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--ink);
    font-family:ui-sans-serif,system-ui,-apple-system,Segoe UI,Roboto,sans-serif;line-height:1.5}
  .wrap{max-width:960px;margin:0 auto;padding:24px}
  h1{font-family:Georgia,serif;font-weight:600;font-size:1.7rem;margin:0 0 2px}
  .sub{color:var(--muted);font-size:.9rem;margin:0 0 20px}
  .sub code{background:var(--chip);padding:1px 6px;border-radius:4px;color:var(--accent)}
  .controls{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:18px}
  button{font:inherit;font-size:.85rem;padding:7px 13px;border-radius:8px;cursor:pointer;
    border:1px solid var(--rule);background:var(--panel);color:var(--ink);transition:.12s}
  button:hover{border-color:var(--accent)}
  button.primary{background:var(--accent);color:#08110b;border-color:var(--accent);font-weight:600}
  button.warn{border-color:var(--brass);color:var(--brass)}
  .grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:12px;margin-bottom:22px}
  .node{background:var(--panel);border:1px solid var(--rule);border-radius:12px;padding:14px;
    border-left:4px solid var(--follower)}
  .node.leader{border-left-color:var(--leader);box-shadow:0 0 0 1px var(--leader) inset}
  .node .id{font-weight:600;font-size:1.05rem;display:flex;justify-content:space-between;align-items:center}
  .role{font-size:.68rem;text-transform:uppercase;letter-spacing:.08em;padding:2px 8px;border-radius:20px;
    background:var(--chip);color:var(--muted)}
  .role.leader{background:var(--leader);color:#08110b}
  .meta{color:var(--muted);font-size:.8rem;margin:6px 0 10px;font-variant-numeric:tabular-nums}
  .meta b{color:var(--ink)}
  .grp{display:inline-block;width:9px;height:9px;border-radius:50%;margin-right:5px;vertical-align:middle}
  .nbtns{display:flex;gap:6px}
  .nbtns button{flex:1;padding:5px}
  .partsel{margin-top:8px;font-size:.78rem;color:var(--muted);display:flex;align-items:center;gap:6px;cursor:pointer}
  .committed{font-size:.85rem;color:var(--muted);margin-bottom:14px}
  .committed b{color:var(--accent);font-variant-numeric:tabular-nums}
  .log{background:#0b0e0c;border:1px solid var(--rule);border-radius:10px;padding:12px 14px;
    font-family:ui-monospace,Menlo,monospace;font-size:.8rem;max-height:220px;overflow:auto}
  .log div{padding:2px 0;border-bottom:1px solid #14181500;color:var(--muted)}
  .log div:first-child{color:var(--ink)}
  .legend{display:flex;gap:16px;flex-wrap:wrap;font-size:.78rem;color:var(--muted);margin:6px 0 20px}
  .legend span{display:flex;align-items:center;gap:6px}
</style>
</head>
<body>
<div class="wrap">
  <h1>Raft Playground</h1>
  <p class="sub">Every button drives the real <code>raft.Cluster</code> in Go. Elect a leader, split the network, watch <b>quorum</b> and <b>fencing</b> behave.</p>

  <div class="controls">
    <button class="warn" onclick="partition()">⚡ Partition selected</button>
    <button onclick="act('/api/heal')">🔗 Heal network</button>
    <button onclick="act('/api/reset')">↺ Reset cluster</button>
  </div>

  <div class="committed">Committed term (the fence line): <b id="ct">—</b></div>
  <div class="grid" id="grid"></div>

  <div class="legend">
    <span><span class="grp" style="background:#5cc48c"></span>Leader</span>
    <span><span class="grp" style="background:#4a5560"></span>Follower</span>
    <span>Colored dot = network group. Same color = can talk to each other.</span>
  </div>

  <div class="log" id="log"></div>
</div>

<script>
const groupColors = ['#5cc48c','#e0664f','#d0a066','#6ea8ff','#c58cf0'];
let selected = new Set();

async function refresh(data){
  if(!data){ data = await (await fetch('/api/state')).json(); }
  document.getElementById('ct').textContent = data.committedTerm;
  const grid = document.getElementById('grid');
  grid.innerHTML = '';
  for(const n of data.nodes){
    const isLeader = n.state === 'Leader';
    const gc = groupColors[n.group % groupColors.length];
    const sel = selected.has(n.id);
    const el = document.createElement('div');
    el.className = 'node' + (isLeader ? ' leader' : '');
    el.innerHTML =
      '<div class="id">Node ' + n.id +
        '<span class="role ' + (isLeader?'leader':'') + '">' + n.state + '</span></div>' +
      '<div class="meta"><span class="grp" style="background:' + gc + '"></span>' +
        'group ' + n.group + ' · term <b>' + n.term + '</b></div>' +
      '<div class="nbtns">' +
        '<button class="primary" onclick="act(\'/api/elect?id=' + n.id + '\')">Elect</button>' +
        '<button onclick="act(\'/api/write?id=' + n.id + '\')">Write</button>' +
      '</div>' +
      '<label class="partsel"><input type="checkbox" ' + (sel?'checked':'') +
        ' onchange="toggle(' + n.id + ')"> isolate this node</label>';
    grid.appendChild(el);
  }
  const log = document.getElementById('log');
  log.innerHTML = data.logs.map(l => '<div>' + l + '</div>').join('');
}
function toggle(id){ selected.has(id) ? selected.delete(id) : selected.add(id); }
async function act(url){ refresh(await (await fetch(url,{method:'POST'})).json()); }
async function partition(){
  if(selected.size===0){ alert('Tick "isolate this node" on the nodes to split off first.'); return; }
  const ids = [...selected].join(',');
  refresh(await (await fetch('/api/partition?ids=' + ids,{method:'POST'})).json());
  selected.clear();
}
refresh();
</script>
</body>
</html>`
