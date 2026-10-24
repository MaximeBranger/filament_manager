// Visionneuse 3D d'un partage : charge le maillage FMM1, l'affiche avec Three.js et applique
// la couleur et la finition de la pastille sélectionnée. Rendu indicatif, sans texture.
import * as THREE from './vendor/three/three.module.min.js';
import { OrbitControls } from './vendor/three/OrbitControls.js';
import { RoomEnvironment } from './vendor/three/RoomEnvironment.js';
import { toCreasedNormals } from './vendor/three/BufferGeometryUtils.js';

const BACKGROUNDS = { light: 0xf2f0ed, dark: 0x1b1a19, night: 0x050505 };
const UNREALISTIC = new Set(['marble', 'wood', 'carbon', 'multicolor', 'OTHER']);

const root = document.getElementById('viewer');
const statusEl = root.querySelector('.viewer-status');
const canvas = root.querySelector('canvas');
const bgButton = document.getElementById('bg-toggle');
const glowWrap = document.getElementById('glow-wrap');
const glowToggle = document.getElementById('glow-toggle');
const finishNote = document.getElementById('finish-note');

function fail(message) {
  statusEl.textContent = message;
  statusEl.hidden = false;
}

// Format FMM1 : "FMM1", nombre de triangles (uint32 LE), positions float32 LE.
function parseFMM(buffer) {
  const view = new DataView(buffer);
  const magic = String.fromCharCode(...new Uint8Array(buffer, 0, 4));
  const count = view.getUint32(4, true);
  if (magic !== 'FMM1' || buffer.byteLength !== 8 + count * 36) throw new Error('format');
  return new Float32Array(buffer, 8, count * 9);
}

// Paillettes simulées : une légère variation de luminosité par triangle (couleurs de sommets).
function glitterColors(count) {
  const colors = new Float32Array(count * 9);
  for (let t = 0; t < count; t++) {
    const k = 0.78 + Math.random() * 0.44;
    colors.fill(k, t * 9, t * 9 + 9);
  }
  return new THREE.BufferAttribute(colors, 3);
}

async function main() {
  let renderer;
  try {
    renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
  } catch (err) {
    fail("Votre navigateur ne permet pas l'affichage 3D.");
    return;
  }
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.toneMapping = THREE.ACESFilmicToneMapping;

  const scene = new THREE.Scene();
  // Le fond suit le thème du système ; le bouton permet de basculer.
  let dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  bgButton.textContent = dark ? 'Fond clair' : 'Fond sombre';
  scene.background = new THREE.Color(dark ? BACKGROUNDS.dark : BACKGROUNDS.light);
  const pmrem = new THREE.PMREMGenerator(renderer);
  scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;

  const camera = new THREE.PerspectiveCamera(40, 1, 0.1, 10000);
  const controls = new OrbitControls(camera, canvas);
  controls.enableDamping = true;

  const keyLight = new THREE.DirectionalLight(0xffffff, 1.2);
  keyLight.position.set(1, 2, 1.5);
  scene.add(keyLight);

  let positions;
  try {
    const resp = await fetch(root.dataset.mesh, { credentials: 'same-origin' });
    if (!resp.ok) throw new Error(String(resp.status));
    positions = parseFMM(await resp.arrayBuffer());
  } catch (err) {
    fail('Impossible de charger le modèle. Rechargez la page.');
    return;
  }
  const triangles = positions.length / 9;

  const raw = new THREE.BufferGeometry();
  raw.setAttribute('position', new THREE.BufferAttribute(positions, 3));
  raw.rotateX(-Math.PI / 2); // STL : Z vers le haut ; Three.js : Y vers le haut
  // Surfaces courbes lissées, arêtes vives (> 30°) conservées.
  const geometry = toCreasedNormals(raw, Math.PI / 6);
  geometry.computeBoundingSphere();

  const material = new THREE.MeshPhysicalMaterial({ color: 0x9e9e9e, side: THREE.DoubleSide });
  const mesh = new THREE.Mesh(geometry, material);
  scene.add(mesh);

  // Cadrage : le modèle tient dans la vue.
  const { center, radius } = geometry.boundingSphere;
  controls.target.copy(center);
  camera.position.set(center.x + radius * 1.7, center.y + radius * 1.1, center.z + radius * 1.7);
  camera.near = radius / 100;
  camera.far = radius * 100;
  camera.updateProjectionMatrix();
  keyLight.position.copy(center).add(new THREE.Vector3(radius * 2, radius * 4, radius * 3));

  let current = null;

  function applyFinish(input) {
    current = input;
    const finish = input.dataset.finish;
    const family = input.dataset.family;
    const p = {
      roughness: 0.55, metalness: 0, clearcoat: 0, clearcoatRoughness: 0, transmission: 0, thickness: 0,
      ior: 1.5, iridescence: 0, emissiveIntensity: 0,
    };
    let vertexColors = false;
    switch (finish) {
      case 'matte': p.roughness = 0.92; break;
      case 'glossy': Object.assign(p, { roughness: 0.2, clearcoat: 1, clearcoatRoughness: 0.05 }); break;
      case 'silk': Object.assign(p, { roughness: 0.28, metalness: 0.45 }); break;
      case 'glitter': Object.assign(p, { roughness: 0.35, metalness: 0.3, clearcoat: 0.6 }); vertexColors = true; break;
      default: break;
    }
    if (family === 'transparent') Object.assign(p, { transmission: 0.85, roughness: 0.12, thickness: radius * 0.2, ior: 1.45 });

    if (vertexColors && !geometry.getAttribute('color')) geometry.setAttribute('color', glitterColors(triangles));
    Object.assign(material, p);
    material.vertexColors = vertexColors;
    material.color.set(input.dataset.hex);
    material.emissive.set(input.dataset.hex);
    material.needsUpdate = true;

    glowWrap.hidden = finish !== 'glow';
    if (finish !== 'glow') glowToggle.checked = false;
    finishNote.hidden = !(UNREALISTIC.has(finish) || input.dataset.unrealistic);
    applyLighting();
  }

  // « Vue dans le noir » pour les filaments phosphorescents.
  function applyLighting() {
    const night = glowToggle.checked && current && current.dataset.finish === 'glow';
    material.emissiveIntensity = night ? 0.9 : 0;
    scene.environmentIntensity = night ? 0.03 : 1;
    keyLight.intensity = night ? 0.05 : 1.2;
    scene.background.set(night ? BACKGROUNDS.night : (dark ? BACKGROUNDS.dark : BACKGROUNDS.light));
  }

  const inputs = document.querySelectorAll('input[name="product_id"]');
  inputs.forEach((input) => input.addEventListener('change', () => applyFinish(input)));
  const initial = document.querySelector('input[name="product_id"]:checked') || inputs[0];
  if (initial) applyFinish(initial);

  bgButton.addEventListener('click', () => {
    dark = !dark;
    bgButton.textContent = dark ? 'Fond clair' : 'Fond sombre';
    applyLighting();
  });
  glowToggle.addEventListener('change', applyLighting);

  function resize() {
    const w = root.clientWidth;
    const h = root.clientHeight;
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
  }
  new ResizeObserver(resize).observe(root);
  resize();

  statusEl.hidden = true;
  renderer.setAnimationLoop(() => {
    controls.update();
    renderer.render(scene, camera);
  });
}

main();
