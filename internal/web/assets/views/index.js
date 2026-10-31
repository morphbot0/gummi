// views/index.js — the surfaces that plug into the views registry. Each
// module registers itself with registerView on import; app.js imports this
// file once, so adding a surface is one module here and one line below.
import './stacks.js?v=__ASSET_V__'
import './repos.js?v=__ASSET_V__'
import './doctor.js?v=__ASSET_V__'
import './settings.js?v=__ASSET_V__'
import './fleet.js?v=__ASSET_V__'
import './newcard.js?v=__ASSET_V__'
import './schedules.js?v=__ASSET_V__'
import './skills.js?v=__ASSET_V__'
import './profiles.js?v=__ASSET_V__'
import './firstrun.js?v=__ASSET_V__'
