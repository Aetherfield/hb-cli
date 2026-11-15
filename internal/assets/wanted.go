// Code generated from kddb's page/frontend/public/assets/minecraft/1.21.4/manifest.json
// (github.com/LeUKi/kddb, MIT) by a script; DO NOT EDIT. It lists only what hb draws.
// The files themselves are Mojang's: they are not in this repository and hb does
// not ship them. `hb assets fetch` downloads Mojang's client JAR, checks it against
// ClientSHA1, and extracts exactly these files, checking each against SHA256.

package assets

// Pinned client JAR of Minecraft Java 1.21.4.
const (
	Version     = "1.21.4"
	ClientURL   = "https://piston-data.mojang.com/v1/objects/a7e5a6024bfd3cd614625aa05629adf760020304/client.jar"
	ClientSHA1  = "a7e5a6024bfd3cd614625aa05629adf760020304"
	ClientBytes = 28335587
)

// Files are the textures hb uses: Path is where it is kept under the cache
// directory, Source its path inside the JAR.
var Files = []File{
	{Path: "textures/item/emerald.png", Source: "assets/minecraft/textures/item/emerald.png", Bytes: 182, SHA256: "7aa1f12ee48666b2c3ae56fd9884ee95bf315795bfd1fd6e4ac3d3811ae3aacb"},
	{Path: "textures/item/diamond.png", Source: "assets/minecraft/textures/item/diamond.png", Bytes: 208, SHA256: "b27b081fde6a794dfb52d37f4ab2a86931ce127323f786a27cea41d2dbe913a2"},
	{Path: "textures/item/golden_apple.png", Source: "assets/minecraft/textures/item/golden_apple.png", Bytes: 204, SHA256: "de0de35dfe21f5644a9de5b8c46c1154e004c1454e60100ed61e3d3aec8cb972"},
	{Path: "textures/item/iron_pickaxe.png", Source: "assets/minecraft/textures/item/iron_pickaxe.png", Bytes: 182, SHA256: "b211a9b920d8f22ce5451780855155644d14ca7b85ab16d31f9c0b572cdda7ee"},
	{Path: "textures/item/ender_pearl.png", Source: "assets/minecraft/textures/item/ender_pearl.png", Bytes: 206, SHA256: "ff40529856c82317ce24cec4ba384971f56aec0f8ce3756e059d2eb17f3b310a"},
	{Path: "textures/item/ender_eye.png", Source: "assets/minecraft/textures/item/ender_eye.png", Bytes: 214, SHA256: "cb9003416a29bdd869a494467a01dba3403b221b88fe3e0a0dc41906d5d49afa"},
	{Path: "textures/item/end_crystal.png", Source: "assets/minecraft/textures/item/end_crystal.png", Bytes: 226, SHA256: "1a75f035d740b3f4f269bbdddfa4cca59c406a43b6c6212bafadbb26db34afd3"},
	{Path: "textures/item/spyglass.png", Source: "assets/minecraft/textures/item/spyglass.png", Bytes: 211, SHA256: "b09e9b9046db1935cd9c35a2e4e3b7763485ec3effbc5077ebeaec1c28513b43"},
	{Path: "textures/item/coal.png", Source: "assets/minecraft/textures/item/coal.png", Bytes: 207, SHA256: "31a5bf332d28c65e0e83368d4008b31c471023ae391e4533f80cd80ad13f54e0"},
	{Path: "textures/item/stick.png", Source: "assets/minecraft/textures/item/stick.png", Bytes: 136, SHA256: "2b5a6b7202fcd3a45663ccf249fe87de076e8c735e0b6ab8a3bf2a812332b4ff"},
	{Path: "textures/item/trial_key.png", Source: "assets/minecraft/textures/item/trial_key.png", Bytes: 220, SHA256: "40d0aa03f363b94197f9a741ce0b9c70176bcb810b3ce977c5ccb8830cdbad87"},
	{Path: "textures/item/chest_minecart.png", Source: "assets/minecraft/textures/item/chest_minecart.png", Bytes: 265, SHA256: "2ed9c4cf90de90086a7aa5376f74087c050ab2713c500c73f721b60a5c7da7ab"},
	{Path: "textures/item/potion.png", Source: "assets/minecraft/textures/item/potion.png", Bytes: 165, SHA256: "6a09a39bb6c41bc92bd5eca1f51caf5b582a85f68206b164476c8e14a4669416"},
	{Path: "textures/item/potion_overlay.png", Source: "assets/minecraft/textures/item/potion_overlay.png", Bytes: 144, SHA256: "a734ecf66d6a09767592c6b38c453854290d47c987ea122712bad37dca9683e9"},
	{Path: "textures/block/tnt_side.png", Source: "assets/minecraft/textures/block/tnt_side.png", Bytes: 176, SHA256: "950388eb9f86f9882c11e4ef291c4da8416433d9cac8ce2713abfb322f2ca159"},
	{Path: "textures/block/cobweb.png", Source: "assets/minecraft/textures/block/cobweb.png", Bytes: 183, SHA256: "409632826c6b942db77edc8828e3e04f56417e6004cc2e24331d3c1535a2a39f"},
	{Path: "textures/block/lava_still.png", Source: "assets/minecraft/textures/block/lava_still.png", Bytes: 4910, SHA256: "7ba546bb9093670f1277ad39d4e973a693ce362f1854a9db00fdb7ab3e434c3c"},
	{Path: "textures/block/tuff_bricks.png", Source: "assets/minecraft/textures/block/tuff_bricks.png", Bytes: 213, SHA256: "986ec3abddf88ca29c56c59c7e078686c10447f402b4c4bbc6c0d25dda16688e"},
	{Path: "textures/block/ancient_debris_side.png", Source: "assets/minecraft/textures/block/ancient_debris_side.png", Bytes: 207, SHA256: "b357286224a18b152312601f920f83939249b51d1229c9811e3e6aa8659ee57d"},
	{Path: "textures/block/rail.png", Source: "assets/minecraft/textures/block/rail.png", Bytes: 216, SHA256: "37d88051df220b81bfeb5f59add18a3a019307671ac59c68e7c0bdb79c599313"},
	{Path: "textures/block/stone.png", Source: "assets/minecraft/textures/block/stone.png", Bytes: 157, SHA256: "33ac70ba8cb701087642a12c5fd29c1ba3006dc4730e9b014ed3a19ce7cdda7b"},
	{Path: "textures/block/cobblestone.png", Source: "assets/minecraft/textures/block/cobblestone.png", Bytes: 200, SHA256: "bcda95fdf5afa91747657aa198fc0b69ebf44141de30a718075f74045e9a5def"},
	{Path: "textures/block/deepslate.png", Source: "assets/minecraft/textures/block/deepslate.png", Bytes: 185, SHA256: "b4c5e7f5538608a371fd4c1f6b398e4b76f723702d10144432636a85a1a61832"},
	{Path: "textures/block/netherrack.png", Source: "assets/minecraft/textures/block/netherrack.png", Bytes: 213, SHA256: "3c0208b749c5393b9bad9d57ed3d830326a02b8cbd8444e96d4d0d277f85625f"},
	{Path: "textures/block/end_stone.png", Source: "assets/minecraft/textures/block/end_stone.png", Bytes: 206, SHA256: "48eadb631e434beb46f380440db106519da1e692e74954a8bd2c987d6c978d76"},
}
