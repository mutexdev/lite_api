#import <Cocoa/Cocoa.h>

static NSString *const LiteAPIApplicationMenuMarker = @"liteapi.application-menu";

static BOOL LiteAPIApplicationMenuItemIsManaged(NSMenuItem *item) {
    return [item.representedObject isKindOfClass:[NSString class]] &&
        [item.representedObject hasPrefix:LiteAPIApplicationMenuMarker];
}

static void LiteAPIRemoveManagedApplicationMenuItems(NSMenu *applicationMenu) {
    for (NSInteger index = applicationMenu.numberOfItems - 1; index >= 0; index--) {
        NSMenuItem *item = [applicationMenu itemAtIndex:index];
        if (LiteAPIApplicationMenuItemIsManaged(item)) {
            [applicationMenu removeItemAtIndex:index];
        }
    }
}

static NSMenuItem *LiteAPIFindSettingsMenuItem(NSMenu *mainMenu, NSMenu **parentMenu) {
    for (NSMenuItem *topLevelItem in mainMenu.itemArray) {
        NSMenu *submenu = topLevelItem.submenu;
        if (submenu == nil) {
            continue;
        }
        for (NSMenuItem *item in submenu.itemArray) {
            if ([item.title isEqualToString:@"Settings…"] && [item.keyEquivalent isEqualToString:@","]) {
                if (parentMenu != NULL) {
                    *parentMenu = submenu;
                }
                return item;
            }
        }
    }
    return nil;
}

static NSInteger LiteAPIApplicationMenuInsertIndex(NSMenu *applicationMenu) {
    for (NSInteger index = 0; index < applicationMenu.numberOfItems; index++) {
        NSMenuItem *item = [applicationMenu itemAtIndex:index];
        if (![item.title hasPrefix:@"About "]) {
            continue;
        }
        NSInteger nextIndex = index + 1;
        if (nextIndex < applicationMenu.numberOfItems && [[applicationMenu itemAtIndex:nextIndex] isSeparatorItem]) {
            nextIndex++;
        }
        return nextIndex;
    }
    return 0;
}

static NSMenu *LiteAPIServicesMenu(void) {
    NSMenu *servicesMenu = NSApp.servicesMenu;
    if (servicesMenu == nil) {
        servicesMenu = [[[NSMenu alloc] initWithTitle:@"Services"] autorelease];
        NSApp.servicesMenu = servicesMenu;
    }
    return servicesMenu;
}

static NSMenuItem *LiteAPIApplicationMenuItem(NSString *title, NSString *marker) {
    NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title action:nil keyEquivalent:@""];
    item.representedObject = [LiteAPIApplicationMenuMarker stringByAppendingString:marker];
    return item;
}

static void LiteAPIInstallApplicationMenuOnMainThread(void) {
    NSMenu *mainMenu = NSApp.mainMenu;
    if (mainMenu == nil || mainMenu.numberOfItems == 0) {
        return;
    }
    NSMenu *applicationMenu = [mainMenu itemAtIndex:0].submenu;
    if (applicationMenu == nil) {
        return;
    }

    NSMenu *settingsParentMenu = nil;
    NSMenuItem *settingsItem = LiteAPIFindSettingsMenuItem(mainMenu, &settingsParentMenu);
    if (settingsItem == nil || settingsParentMenu == nil) {
        return;
    }
    [settingsItem retain];
    [settingsParentMenu removeItem:settingsItem];
    LiteAPIRemoveManagedApplicationMenuItems(applicationMenu);

    NSInteger insertIndex = LiteAPIApplicationMenuInsertIndex(applicationMenu);
    [applicationMenu insertItem:settingsItem atIndex:insertIndex++];
    [settingsItem release];

    NSMenuItem *settingsSeparator = [NSMenuItem separatorItem];
    settingsSeparator.representedObject = [LiteAPIApplicationMenuMarker stringByAppendingString:@".settings-separator"];
    [applicationMenu insertItem:settingsSeparator atIndex:insertIndex++];

    NSMenuItem *servicesItem = LiteAPIApplicationMenuItem(@"Services", @".services");
    servicesItem.submenu = LiteAPIServicesMenu();
    [applicationMenu insertItem:servicesItem atIndex:insertIndex++];
    [servicesItem release];

    NSMenuItem *servicesSeparator = [NSMenuItem separatorItem];
    servicesSeparator.representedObject = [LiteAPIApplicationMenuMarker stringByAppendingString:@".services-separator"];
    [applicationMenu insertItem:servicesSeparator atIndex:insertIndex];
}

void liteapiInstallApplicationMenu(void) {
    if ([NSThread isMainThread]) {
        LiteAPIInstallApplicationMenuOnMainThread();
        return;
    }
    dispatch_sync(dispatch_get_main_queue(), ^{
        LiteAPIInstallApplicationMenuOnMainThread();
    });
}
