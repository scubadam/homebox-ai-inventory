# Visual Walkthrough: Scanning a Drawer End-to-End

This walkthrough shows how **Homebox AI Inventory** turns a single smartphone photo of a cluttered drawer into a structured, searchable Homebox container with categorized items, quantities, and rich descriptions.

---

## 1. Choose Workflow & Target Container

Select **`New Box`** (or **`Re-Inventory`** / **`Add Items`**), pick an optional **Parent Location** (e.g., `Kitchen`), set the **Container Type** (`Box`, `Draw`, etc.), and give it a name.

<p align="center">
  <table>
    <tr>
      <td align="center"><img src="images/01-home-screen.png" width="240" alt="Home screen showing workflow tabs and photo options"><br><sub><b>1a.</b> Clean mobile-first PWA landing screen</sub></td>
      <td align="center"><img src="images/02-target-container.png" width="240" alt="Target container configured as Kitchen / Draw / Random kitchen draw"><br><sub><b>1b.</b> Select parent location, type & container name</sub></td>
    </tr>
  </table>
</p>

---

## 2. Capture Photos & Scan with AI Vision

Snap one or more angles directly with the camera or upload from your phone's photo library. High-res smartphone photos are resized in the browser (~250KB) before upload, and you can star (`⭐`) any angle as the primary Homebox cover photo.

<p align="center">
  <table>
    <tr>
      <td align="center"><img src="images/03-photo-ready.jpg" width="240" alt="Photo attached with Cover and Angle 1 badges and Scan with Gemini button"><br><sub><b>2a.</b> Photo attached & marked as Cover; tap <b>Scan with Gemini</b></sub></td>
      <td align="center"><img src="images/04-ai-synthesizing.png" width="240" alt="Gemini synthesizing angles and counting items"><br><sub><b>2b.</b> AI vision analyzes, deduplicates & counts items</sub></td>
    </tr>
  </table>
</p>

---

## 3. Review, Edit & Save to Homebox

Review every detected item before committing—adjust quantities with **`-` / `+`**, switch or create categories on the fly with **`+ Type`**, or tweak the AI-generated descriptions. When ready, tap **`Save to Homebox`**.

<p align="center">
  <table>
    <tr>
      <td align="center"><img src="images/05-review-items.jpg" width="210" alt="AI detected items list with quantities and categories"><br><sub><b>3a.</b> Detected items, counts & categories</sub></td>
      <td align="center"><img src="images/06-edit-descriptions.jpg" width="210" alt="Inline editing of item descriptions"><br><sub><b>3b.</b> Editable names, counts & descriptions</sub></td>
      <td align="center"><img src="images/07-save-to-homebox.jpg" width="210" alt="Save to Homebox button at bottom of review list"><br><sub><b>3c.</b> One-tap commit to Homebox</sub></td>
      <td align="center"><img src="images/08-saved-confirmation.png" width="210" alt="Saved confirmation card with Open in Homebox button"><br><sub><b>3d.</b> Sync summary & direct <b>Open in Homebox</b> link</sub></td>
    </tr>
  </table>
</p>

---

## 4. Instant Result in Homebox

Tapping **Open in Homebox** jumps straight to the newly created container under `Kitchen > Random kitchen draw`, complete with the cover photo and all 5 cataloged items ready to search.

<p align="center">
  <table>
    <tr>
      <td align="center"><img src="images/09-homebox-container.png" width="220" alt="Homebox container view showing Kitchen > Random kitchen draw"><br><sub><b>4a.</b> Container created under parent location</sub></td>
      <td align="center"><img src="images/10-homebox-photo-and-items.jpg" width="220" alt="Homebox container showing attached photo and item cards"><br><sub><b>4b.</b> Cover photo & indexed items</sub></td>
      <td align="center"><img src="images/11-homebox-item-details.png" width="220" alt="Homebox item cards with quantities and descriptions"><br><sub><b>4c.</b> Searchable quantities & detailed descriptions</sub></td>
    </tr>
  </table>
</p>
